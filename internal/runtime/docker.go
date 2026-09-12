package runtime

import (
	"context"
	"fmt"
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

// EnvPair is one KEY=VALUE injected at `docker run`.
//
// This type lives in runtime (not store) so Docker isolation does not
// import the persistence package. The worker maps store.EnvVar here
// after store validation; Run validates again so a buggy caller cannot
// smuggle PORT or a newline into argv.
type EnvPair struct {
	Key   string
	Value string
}

// ContainerName is the docker --name for a deployment. Hyphens are
// stripped so the name stays a single DNS-ish token.
func ContainerName(deploymentID string) string {
	return "forge-run-" + strings.ReplaceAll(deploymentID, "-", "")
}

// Builder turns a source directory into a local image name.
//
// The returned string is docker build's combined output (truncated by
// the worker before Postgres). Tests can return a stub log. A build
// failure still returns whatever output was captured so the dashboard
// can show why docker build died.
type Builder interface {
	Build(ctx context.Context, dir, image, kind string) (buildLog string, err error)
}

// Runner starts and stops isolated containers. Implementations must not
// mount the host Docker socket into the workload.
type Runner interface {
	Run(ctx context.Context, image, containerName string, env []EnvPair) (Instance, error)
	Stop(ctx context.Context, containerName string) error
	Logs(ctx context.Context, containerName string, tail int) (string, error)
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

func (HostDocker) Build(ctx context.Context, dir, image, kind string) (string, error) {
	if err := prepareDockerfile(dir, kind); err != nil {
		return "", err
	}
	if err := validateImageName(image); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "docker", "build", "-t", image, "--", dir)
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

func (HostDocker) Run(ctx context.Context, image, containerName string, env []EnvPair) (Instance, error) {
	if err := validateImageName(image); err != nil {
		return Instance{}, err
	}
	if err := validateContainerName(containerName); err != nil {
		return Instance{}, err
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
	// - user env first, then PORT=8080 so Forge wins if a key collides
	publish := "127.0.0.1:" + strconv.Itoa(port) + ":8080"
	args, err := dockerRunArgs(image, containerName, publish, env)
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
func dockerRunArgs(image, containerName, publish string, env []EnvPair) ([]string, error) {
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
	args = append(args, "-e", "PORT=8080", "-p", publish, "--", image)
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

func prepareDockerfile(dir, kind string) error {
	switch detect.Kind(kind) {
	case detect.KindDockerfile:
		return nil
	case detect.KindNode:
		return writeIfAbsent(filepath.Join(dir, "Dockerfile"), nodeDockerfile)
	case detect.KindGo:
		return writeIfAbsent(filepath.Join(dir, "Dockerfile"), goDockerfile)
	default:
		return fmt.Errorf("docker build: unknown kind %q", kind)
	}
}

func writeIfAbsent(path, body string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(body), 0o600)
}

// Forge-owned Dockerfiles. npm/go run inside the build container, not
// on the control-plane host. They are still untrusted execution — just
// isolated.
const nodeDockerfile = `FROM node:20-alpine
WORKDIR /app
COPY . .
RUN npm install --omit=dev
ENV PORT=8080
EXPOSE 8080
CMD ["npm","start"]
`

const goDockerfile = `FROM golang:1.22-alpine
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -o /app/app .
ENV PORT=8080
EXPOSE 8080
CMD ["/app/app"]
`

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
	if strings.EqualFold(p.Key, "PORT") {
		return fmt.Errorf("PORT is reserved by Forge")
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
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("health: timed out waiting for %s", url)
		}
		reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		req, err := newGET(reqCtx, url)
		if err != nil {
			cancel()
			return err
		}
		resp, err := httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			cancel()
			if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
				return nil
			}
		} else {
			cancel()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
}
