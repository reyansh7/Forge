package runtime

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/detect"
)

// EnvPair is one KEY=VALUE injected at `docker build` and `docker run`.
//
// This type lives in runtime (not store) so Docker isolation does not
// import the persistence package. The worker maps store.EnvVar here
// after store validation; Run validates again so a buggy caller cannot
// smuggle PORT or a newline into argv.
type EnvPair struct {
	Key   string
	Value string
}

// Builder turns a source directory into a local image name.
//
// kind is the pack ID from detect (runtime_kind). HostDocker re-runs
// detect.Tree and writes a Forge Dockerfile when the repo has none.
//
// The returned string is docker build's combined output (truncated by
// the worker before Postgres). Tests can return a stub log. A build
// failure still returns whatever output was captured so the dashboard
// can show why docker build died.
//
// env is applied at build time (Vite/Next/CRA public keys) and again
// at docker run for server processes. Values must not be written into
// build_log by Forge; docker itself may echo ARG names.
type Builder interface {
	Build(ctx context.Context, dir, image, kind string, env []EnvPair) (buildLog string, err error)
}

// Runner starts and stops isolated containers. Implementations must not
// mount the host Docker socket into the workload.
type Runner interface {
	// containerPort is the listen port inside the image (EXPOSE / pack
	// default). 0 means detect.DefaultPort. The host publish port is
	// allocated separately and stored on the deployment.
	Run(ctx context.Context, image, containerName string, env []EnvPair, containerPort int) (Instance, error)
	Stop(ctx context.Context, containerName string) error
	Logs(ctx context.Context, containerName string, tail int) (string, error)
	// Inspect is required so the worker can fail a dead container
	// before the health loop. Tests return a stub running state.
	Inspect(ctx context.Context, containerName string) (ContainerState, error)
}

// Running reports whether a named container is currently up.
// Missing containers are not an error: they are simply not running.
//
// The dashboard polls application health on this, not HTTP GET to the
// published port. Inspect does not generate access-log lines in the app.
func (HostDocker) Running(ctx context.Context, containerName string) (bool, error) {
	if err := validateContainerName(containerName); err != nil {
		return false, err
	}
	cmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", "--", containerName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// Instance is a running workload as seen from the host.
type Instance struct {
	ContainerID string
	HostPort    int
}

// HostDocker talks to the Docker CLI with argv only (no shell).
//
// The worker is the control plane: it may invoke docker. User code runs
// *inside* build and run, not as `sh -c` on the host.
type HostDocker struct{}

func (HostDocker) Build(ctx context.Context, dir, image, kind string, env []EnvPair) (string, error) {
	if err := prepareDockerfile(dir, kind); err != nil {
		return "", err
	}
	if err := writeProductionEnv(dir, env); err != nil {
		return "", err
	}
	if err := validateImageName(image); err != nil {
		return "", err
	}
	args := []string{"build", "-t", image}
	for _, ev := range env {
		if err := validateEnvPair(ev); err != nil {
			return "", err
		}
		// --build-arg is for operator Dockerfiles that declare ARG.
		// Vite/CRA/Next read .env.production.local from the context
		// (written above). Do not log these values.
		args = append(args, "--build-arg", ev.Key+"="+ev.Value)
	}
	args = append(args, "--", dir)
	cmd := exec.CommandContext(ctx, "docker", args...)
	// docker build runs the Dockerfile as untrusted execution inside
	// the build container. We do not pass --network=host, --privileged,
	// or -v /var/run/docker.sock. The build can still reach the public
	// internet to pull base images (needed for FROM). That is a supply-
	// chain risk the operator accepts by deploying the repo; it is not
	// a path onto the control-plane Postgres/Redis sockets, which stay
	// published on loopback only.
	out, err := cmd.CombinedOutput()
	log := string(out)
	if err != nil {
		return log, fmt.Errorf("docker build: %s", truncate(log, 400))
	}
	return log, nil
}

func (HostDocker) Run(ctx context.Context, image, containerName string, env []EnvPair, containerPort int) (Instance, error) {
	if err := validateImageName(image); err != nil {
		return Instance{}, err
	}
	if err := validateContainerName(containerName); err != nil {
		return Instance{}, err
	}
	if containerPort < 1 || containerPort > 65535 {
		containerPort = detect.DefaultPort
	}

	port, err := freeLoopbackPort()
	if err != nil {
		return Instance{}, err
	}

	// Isolation (Phase 0 limits + Phase 3 hardening):
	// - publish on 127.0.0.1 only (not 0.0.0.0)
	// - memory / cpu / pids caps
	// - cap-drop ALL, tmpfs /tmp, pull=never
	// - no --privileged, no volume mounts, no Docker socket
	// - user env first, then PORT=<detected> so Forge wins if a key collides
	publish := "127.0.0.1:" + strconv.Itoa(port) + ":" + strconv.Itoa(containerPort)
	args, err := dockerRunArgs(image, containerName, publish, env, containerPort)
	if err != nil {
		return Instance{}, err
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Instance{}, fmt.Errorf("docker run: %s", truncate(string(out), 400))
	}
	id := strings.TrimSpace(string(out))
	if len(id) > 64 {
		id = id[:64]
	}
	return Instance{ContainerID: id, HostPort: port}, nil
}

// dockerRunArgs is the argv after "docker" for a workload container.
//
// Phase 3 isolation on top of Phase 0 limits:
//   - --cap-drop ALL: the process cannot use Linux capabilities
//     (NET_ADMIN, SYS_ADMIN, …) even if the image's user is root.
//   - tmpfs /tmp: writable scratch that is not a host bind mount.
//   - --pull never: the tag must already exist locally (we just built
//     it, or rollback reused it). A typo must not pull from a registry.
//
// Still forbidden: --privileged, Docker socket mounts, host network,
// publishing on 0.0.0.0. Read-only rootfs is not set — many images
// write under /app (pyc, npm) and would fail closed incorrectly.
func dockerRunArgs(image, containerName, publish string, env []EnvPair, containerPort int) ([]string, error) {
	args := []string{
		"run", "-d",
		"--name", containerName,
		"--memory", "256m",
		"--cpus", "0.5",
		"--pids-limit", "256",
		"--security-opt", "no-new-privileges",
		"--cap-drop", "ALL",
		"--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
		"--pull", "never",
	}
	for _, ev := range env {
		if err := validateEnvPair(ev); err != nil {
			return nil, err
		}
		args = append(args, "-e", ev.Key+"="+ev.Value)
	}
	if containerPort < 1 || containerPort > 65535 {
		containerPort = detect.DefaultPort
	}
	args = append(args, "-e", "HOST=0.0.0.0", "-e", "PORT="+strconv.Itoa(containerPort), "-p", publish, "--", image)
	return args, nil
}

func (HostDocker) Stop(ctx context.Context, containerName string) error {
	if err := validateContainerName(containerName); err != nil {
		return err
	}
	// Best-effort: a missing container is not a pipeline failure.
	// -t 2: default docker stop waits 10s for SIGTERM. That outlived
	// the HTTP timeout and skipped persisting STOPPED (LIVE + dead).
	stop := exec.CommandContext(ctx, "docker", "stop", "-t", "2", "--", containerName)
	_ = stop.Run()
	rm := exec.CommandContext(ctx, "docker", "rm", "-f", "--", containerName)
	_ = rm.Run()
	return nil
}

func (HostDocker) Logs(ctx context.Context, containerName string, tail int) (string, error) {
	if err := validateContainerName(containerName); err != nil {
		return "", err
	}
	if tail < 1 {
		tail = 100
	}
	if tail > 500 {
		tail = 500
	}
	cmd := exec.CommandContext(ctx, "docker", "logs", "-t", "--tail", strconv.Itoa(tail), "--", containerName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker logs: %s", truncate(string(out), 300))
	}
	text := string(out)
	if len(text) > 64*1024 {
		text = text[len(text)-64*1024:]
	}
	return text, nil
}

// dockerLogsFollowArgs is `docker logs -f` for the SSE stream.
//
// -f follows until the context is cancelled. The API owns the deadline
// (client disconnect or a max stream time). -- is the name boundary so
// a crafted container name cannot become another docker flag.
func dockerLogsFollowArgs(containerName string, tail int) ([]string, error) {
	if err := validateContainerName(containerName); err != nil {
		return nil, err
	}
	if tail < 1 {
		tail = 100
	}
	if tail > 500 {
		tail = 500
	}
	return []string{"logs", "-f", "-t", "--tail", strconv.Itoa(tail), "--", containerName}, nil
}

// FollowLogs streams `docker logs -f` lines to write until ctx ends.
//
// This is the Phase 4 live-tail. It is still an operator control-plane
// read, not a privileged host agent. write must not block forever —
// a stuck SSE client should cancel ctx. Lines are not redacted: they
// are whatever the workload printed. Authorization happens in HTTP.
func (HostDocker) FollowLogs(ctx context.Context, containerName string, tail int, write func(line string) error) error {
	args, err := dockerLogsFollowArgs(containerName, tail)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return fmt.Errorf("docker logs follow: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		_ = pw.Close()
	}()

	scanner := bufio.NewScanner(pr)
	// App log lines can be large; the default 64KiB token is tight.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if err := write(scanner.Text()); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("docker logs follow read: %w", err)
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
	if ctx.Err() != nil {
		return nil
	}
	return nil
}

func prepareDockerfile(dir, kind string) error {
	// Re-inspect the confined tree. The worker already ran detect.Tree;
	// doing it again keeps Builder’s signature (kind string) stable and
	// ensures the recipe matches the files we are about to COPY.
	spec, err := detect.Tree(dir)
	if err != nil {
		if kind != "" {
			return fmt.Errorf("docker build: %w", err)
		}
		return err
	}
	if err := writeExtraFiles(dir, spec.ExtraFiles); err != nil {
		return err
	}
	if spec.GeneratedDockerfile == "" {
		return nil
	}
	return writeIfAbsent(filepath.Join(dir, "Dockerfile"), spec.GeneratedDockerfile)
}

func writeExtraFiles(dir string, files map[string]string) error {
	for name, body := range files {
		// Basenames only. A generated sidecar must not escape the
		// confined build directory via ../ or a slash.
		if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
			return fmt.Errorf("docker build: refused generated file %q", name)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func writeIfAbsent(path, body string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(body), 0o600)
}

func validateImageName(name string) error {
	if name == "" || strings.ContainsAny(name, " \t\n/:@") {
		return fmt.Errorf("invalid image name")
	}
	return nil
}

func validateContainerName(name string) error {
	if name == "" || strings.ContainsAny(name, " \t\n/") {
		return fmt.Errorf("invalid container name")
	}
	return nil
}

func validateEnvPair(p EnvPair) error {
	if p.Key == "" || !envKeyPattern.MatchString(p.Key) {
		return fmt.Errorf("invalid env key")
	}
	if strings.EqualFold(p.Key, "PORT") || strings.EqualFold(p.Key, "HOST") {
		return fmt.Errorf("%s is reserved by Forge", strings.ToUpper(p.Key))
	}
	if strings.HasPrefix(strings.ToUpper(p.Key), "FORGE_") {
		return fmt.Errorf("FORGE_ keys are reserved")
	}
	if strings.ContainsAny(p.Value, "\x00\r\n") {
		return fmt.Errorf("invalid env value")
	}
	if len(p.Value) > 4096 {
		return fmt.Errorf("env value too long")
	}
	return nil
}

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate port: %w", err)
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("allocate port: unexpected addr")
	}
	return addr.Port, nil
}

// HealthGET polls http://127.0.0.1:{port}/ until a 2xx or timeout.
// The worker is on the host, so loopback is the published mapping.
// This is a one-shot deploy gate. After LIVE, the API must not keep
// GETting the app — that would flood docker logs with control-plane probes.
func HealthGET(ctx context.Context, port int, timeout time.Duration) error {
	return HealthGETPath(ctx, port, "/", timeout)
}

// HealthGETPath is HealthGET with an operator-configured path.
//
// path is appended to loopback; it is never used as a full URL. The
// store already rejected "://". We reject it again so a corrupted
// health_path cannot turn this probe into SSRF.
func HealthGETPath(ctx context.Context, port int, path string, timeout time.Duration) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("health: invalid port")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "://") || strings.ContainsAny(path, "\r\n\x00 ") {
		return fmt.Errorf("health: invalid path")
	}
	return ProbeHTTP(ctx, Probe{Port: port, Path: path, Timeout: timeout})
}
