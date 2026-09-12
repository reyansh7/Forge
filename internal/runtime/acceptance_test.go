package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/detect"
)

// TestAcceptanceGitHubTrees clones the two repos that failed in the
// dashboard and runs detect (always) plus a real docker build/run for
// the Vite portfolio when FORGE_ACCEPTANCE=1 and docker is available.
func TestAcceptanceGitHubTrees(t *testing.T) {
	if os.Getenv("FORGE_ACCEPTANCE") == "" {
		t.Skip("set FORGE_ACCEPTANCE=1 to clone GitHub and run docker")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	t.Run("portfolio_vite_static", func(t *testing.T) {
		dir := cloneShallow(t, "https://github.com/reyansh7/My_Portfolio.git")
		spec, err := detect.Tree(dir)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Runtime != detect.RuntimeStatic || spec.Framework != "vite-react" {
			t.Fatalf("detect %+v", spec)
		}
		if !strings.Contains(spec.GeneratedDockerfile, "run build -- --base ./") {
			t.Fatalf("vite must use relative base:\n%s", spec.GeneratedDockerfile)
		}
		if os.Getenv("FORGE_ACCEPTANCE_DOCKER") == "" {
			if _, err := exec.LookPath("docker"); err != nil {
				t.Log("docker not on PATH; detect-only")
				return
			}
		}
		runDetected(t, dir, spec, "accept-portfolio")
	})

	t.Run("express_hardcoded_port", func(t *testing.T) {
		dir := cloneShallow(t, "https://github.com/bradtraversy/react_express_starter.git")
		spec, err := detect.Tree(dir)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Framework != "express" || spec.Port != 5000 || spec.PortSource != "source_listen" {
			t.Fatalf("detect %+v", spec)
		}
		if spec.HealthPath != "/api/customers" {
			t.Fatalf("health path %q", spec.HealthPath)
		}
		if spec.Runtime != detect.RuntimeServer {
			t.Fatalf("runtime %q", spec.Runtime)
		}
		if _, err := exec.LookPath("docker"); err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		d := HostDocker{}
		image := "forge-accept-express"
		log, err := d.Build(ctx, dir, image, string(spec.Kind), nil)
		if err != nil {
			t.Fatalf("build: %v\n%s", err, truncate(log, 800))
		}
		name := "accept-express-starter"
		_ = d.Stop(ctx, name)
		inst, err := d.Run(ctx, image, name, nil, spec.ListenPort())
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		t.Cleanup(func() { _ = d.Stop(context.Background(), name) })
		err = ProbeHTTP(ctx, Probe{
			Port: inst.HostPort, Path: "/", Timeout: 20 * time.Second,
			ListenPort: spec.ListenPort(), PortSource: spec.PortSource, Container: name,
			Inspect: func(ctx context.Context) (ContainerState, error) { return d.Inspect(ctx, name) },
		})
		if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
			logs, _ := d.Logs(ctx, name, 40)
			t.Fatalf("GET / should still 404, got %v\n%s", err, logs)
		}
		if err := ProbeHTTP(ctx, Probe{
			Port: inst.HostPort, Path: spec.HealthOrDefault(), Timeout: 10 * time.Second,
			ListenPort: spec.ListenPort(), PortSource: spec.PortSource, Container: name,
			Inspect: func(ctx context.Context) (ContainerState, error) { return d.Inspect(ctx, name) },
		}); err != nil {
			logs, _ := d.Logs(ctx, name, 40)
			t.Fatalf("inferred health %s: %v\n%s", spec.HealthOrDefault(), err, logs)
		}
		t.Logf("GET / 404; inferred health %s ok; port=%d", spec.HealthOrDefault(), spec.Port)
	})
}

func cloneShallow(t *testing.T, url string) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "clone", "--depth", "1", "--", url, dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	return dir
}

func runDetected(t *testing.T, dir string, spec detect.Result, slug string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	d := HostDocker{}
	image := "forge-accept-" + slug
	log, err := d.Build(ctx, dir, image, string(spec.Kind), nil)
	if err != nil {
		t.Fatalf("build: %v\n%s", err, truncate(log, 800))
	}
	name := "accept-" + slug
	_ = d.Stop(ctx, name)
	inst, err := d.Run(ctx, image, name, nil, spec.ListenPort())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background(), name) })
	st, err := d.Inspect(ctx, name)
	if err != nil || !st.Running {
		logs, _ := d.Logs(ctx, name, 80)
		t.Fatalf("inspect %+v err=%v\n%s", st, err, logs)
	}
	if err := ProbeHTTP(ctx, Probe{
		Port: inst.HostPort, Path: "/", Timeout: 45 * time.Second,
		ListenPort: spec.ListenPort(), PortSource: spec.PortSource,
		Container: name,
		Inspect:   func(ctx context.Context) (ContainerState, error) { return d.Inspect(ctx, name) },
	}); err != nil {
		logs, _ := d.Logs(ctx, name, 80)
		t.Fatalf("health: %v\n%s", err, logs)
	}
	t.Logf("LIVE container=%s id=%s host_port=%d listen=%d", name, inst.ContainerID, inst.HostPort, spec.ListenPort())
	_ = filepath.ToSlash(dir)
	if !strings.Contains(log, "built") && !strings.Contains(strings.ToLower(log), "successfully") {
		t.Logf("build log tail: %s", truncate(log, 200))
	}
}
