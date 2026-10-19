package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/reyansh7/Forge/internal/detect"
)

// contextDone matches BuildKit's final line for one context transfer.
// The size token is what the daemon received, not the workspace quota.
var contextDone = regexp.MustCompile(`transferring context: (\S+) ([\d.]+)s done`)

func TestDockerContextDropsCachesKeepsDatasets(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skip("docker info: " + strings.TrimSpace(string(out)))
	}

	beforeDir := t.TempDir()
	nodeDir := t.TempDir()
	pythonDir := t.TempDir()
	writeComparableTree(t, beforeDir)
	writeComparableTree(t, nodeDir)
	writeComparableTree(t, pythonDir)
	if err := mergeBuildIgnore(nodeDir, detect.KindNode, true); err != nil {
		t.Fatal(err)
	}
	if err := mergeBuildIgnore(pythonDir, detect.KindPython, true); err != nil {
		t.Fatal(err)
	}

	beforeImg := "forge-context-before-test"
	nodeImg := "forge-context-node-test"
	pythonImg := "forge-context-python-test"
	t.Cleanup(func() {
		_ = exec.Command("docker", "rmi", "-f", beforeImg, nodeImg, pythonImg).Run()
	})

	beforeLog := dockerBuildFixture(t, beforeDir, beforeImg)
	nodeLog := dockerBuildFixture(t, nodeDir, nodeImg)
	pythonLog := dockerBuildFixture(t, pythonDir, pythonImg)
	beforeSize, beforeSec := mustContext(t, beforeLog)
	nodeSize, nodeSec := mustContext(t, nodeLog)
	pythonSize, pythonSec := mustContext(t, pythonLog)
	t.Logf("context before=%d bytes (%.3fs) node=%d bytes (%.3fs) python=%d bytes (%.3fs)", beforeSize, beforeSec, nodeSize, nodeSec, pythonSize, pythonSec)
	if nodeSize >= beforeSize || pythonSize >= beforeSize {
		t.Fatalf("context did not shrink: before %d node %d python %d", beforeSize, nodeSize, pythonSize)
	}
	assertImage(t, nodeImg, "test -s /app/data/resumes.csv && test -s /app/saved_models/model.joblib && test -s /app/assets/logo.png && test ! -e /app/node_modules && test ! -e /app/.git && test -s /app/.venv/lib/site.py")
	assertImage(t, pythonImg, "test -s /app/data/resumes.csv && test -s /app/saved_models/model.joblib && test -s /app/assets/logo.png && test ! -e /app/.venv && test ! -e /app/.git && test ! -e /app/__pycache__/app.pyc && test -s /app/node_modules/leftpad/index.js")
}

func writeComparableTree(t *testing.T, dir string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "Dockerfile"), "FROM alpine:3.20\nWORKDIR /app\nCOPY . .\n")
	mustWrite(t, filepath.Join(dir, "package.json"), `{"scripts":{"start":"node server.js"}}`+"\n")
	mustWrite(t, filepath.Join(dir, "server.js"), "console.log('ok')\n")
	blob := bytesOf('a', 256*1024)
	mustWrite(t, filepath.Join(dir, ".git", "objects", "pack", "pack.bin"), blob)
	mustWrite(t, filepath.Join(dir, "node_modules", "leftpad", "index.js"), blob)
	mustWrite(t, filepath.Join(dir, ".venv", "lib", "site.py"), blob)
	mustWrite(t, filepath.Join(dir, "__pycache__", "app.pyc"), blob)
	mustWrite(t, filepath.Join(dir, "data", "resumes.csv"), blob)
	mustWrite(t, filepath.Join(dir, "saved_models", "model.joblib"), blob)
	mustWrite(t, filepath.Join(dir, "assets", "logo.png"), blob)
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func bytesOf(b byte, n int) string {
	return strings.Repeat(string(b), n)
}

func dockerBuildFixture(t *testing.T, dir, image string) string {
	t.Helper()
	cmd := exec.Command("docker", "build", "-t", image, "--", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker build %s: %v\n%s", image, err, out)
	}
	return string(out)
}

func mustContext(t *testing.T, log string) (int64, float64) {
	t.Helper()
	matches := contextDone.FindAllStringSubmatch(log, -1)
	if len(matches) == 0 {
		t.Fatalf("no transferring context line:\n%s", log)
	}
	// The dockerfile transfer is a few hundred bytes. The tree transfer
	// is the large one. Take the greatest parsed size.
	var best int64
	var sec float64
	for _, m := range matches {
		n, err := parseDockerSize(m[1])
		if err != nil {
			t.Fatal(err)
		}
		if n >= best {
			best = n
			sec, err = strconv.ParseFloat(m[2], 64)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return best, sec
}

func assertImage(t *testing.T, image, script string) {
	t.Helper()
	cmd := exec.Command("docker", "run", "--rm", "--entrypoint", "/bin/sh", image, "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("image %s contents: %v\n%s", image, err, out)
	}
}
