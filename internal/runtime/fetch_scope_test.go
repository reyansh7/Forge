package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopeCloneArgsNeedBlobFilter(t *testing.T) {
	if scopeCloneArgs(".") != nil || scopeCloneArgs("") != nil || scopeCloneArgs("/") != nil {
		t.Fatal("a repository root must stay a full shallow clone")
	}
	args := scopeCloneArgs("frontend")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--filter=blob:none") || !strings.Contains(joined, "--sparse") {
		t.Fatalf("service scope must filter blobs, got %v", args)
	}
	if err := validateScopeRoot("../ml-model"); err == nil {
		t.Fatal("parent traversal must be rejected before git runs")
	}
	if err := validateScopeRoot(`frontend\ml`); err == nil {
		t.Fatal("backslash roots must be rejected")
	}
}

func TestScopedCloneOmitsSiblingTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	src := t.TempDir()
	writeFixture := func(rel string, n int) {
		t.Helper()
		path := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Repeat("x", n)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture("svc/app.txt", 64)
	// Incompressible bytes. A run of zeros packs so small that pack
	// size cannot show whether the blob was omitted.
	blob := make([]byte, 256<<10)
	for i := range blob {
		blob[i] = byte(i)
	}
	other := filepath.Join(src, "other", "big.bin")
	if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=forge", "GIT_AUTHOR_EMAIL=forge@example.com", "GIT_COMMITTER_NAME=forge", "GIT_COMMITTER_EMAIL=forge@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s", strings.Join(args, " "), out)
		}
	}
	git("init")
	// GitHub advertises this. A local source does not, and the client
	// then warns "filtering not recognized by server, ignoring" and
	// downloads every blob. The capability has to be on before the
	// assertion can claim a smaller transfer.
	git("config", "uploadpack.allowFilter", "true")
	git("add", ".")
	git("commit", "-m", "fixture")

	dest := filepath.Join(t.TempDir(), "out")
	// A path clone ignores --filter and --depth (Git says so). file://
	// is a real transport, which is the same class of clone as the
	// production https remote. Production FetchScope still rejects
	// file://; this fixture only proves the argv. protocol.file.allow
	// is relaxed for that fixture URL and nowhere else.
	remote := "file:///" + filepath.ToSlash(src)
	args := append(gitCommandConfig(), "clone", "--depth", "1")
	args = append(args, scopeCloneArgs("svc")...)
	for i, arg := range args {
		if arg == "protocol.file.allow=never" {
			args[i] = "protocol.file.allow=always"
		}
	}
	args = append(args, "--", remote, dest)
	cmd := exec.Command("git", args...)
	cmd.Env = gitSafeEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %s", out)
	}
	// applySparseCone keeps protocol.file.allow=never, which is correct
	// for an https origin. This fixture's origin is file://, so the
	// follow-up fetch that fills in the service cone has to allow it.
	// The argv is otherwise the same command applySparseCone runs.
	cone := exec.Command("git", "-c", "protocol.file.allow=always", "-C", dest, "sparse-checkout", "set", "--cone", "--", "svc")
	cone.Env = gitSafeEnv()
	if out, err := cone.CombinedOutput(); err != nil {
		t.Fatalf("sparse-checkout: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dest, "svc", "app.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "other", "big.bin")); !os.IsNotExist(err) {
		t.Fatalf("sibling blob was checked out: %v", err)
	}
	// cat-file on a partial clone lazily downloads a missing blob. Disable
	// that so the assertion measures what the clone already transferred.
	missing := exec.Command("git", "-C", dest, "cat-file", "-e", "HEAD:other/big.bin")
	missing.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1")
	if err := missing.Run(); err == nil {
		t.Fatal("sparse checkout hid the file but the blob was still transferred")
	}
	present := exec.Command("git", "-C", dest, "cat-file", "-e", "HEAD:svc/app.txt")
	present.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1")
	if err := present.Run(); err != nil {
		t.Fatal("the service file blob must be present without a later fetch")
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Fatal("scoped checkout must still leave .git for the existing cleanup")
	}
	var measured int64
	_ = filepath.Walk(dest, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || !info.Mode().IsRegular() {
			return nil
		}
		if !strings.Contains(path, string(filepath.Separator)+".git"+string(filepath.Separator)) && filepath.Base(path) != ".git" {
			measured += info.Size()
		}
		return nil
	})
	t.Logf("scoped working tree regular bytes=%d sibling_blob=%d", measured, 256<<10)
	if measured >= 256<<10 {
		t.Fatalf("working tree %d still includes the sibling blob", measured)
	}
}

func TestApplySparseConeKeepsServiceFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "svc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "other"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "svc", "app.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "other", "skip.txt"), []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=forge", "GIT_AUTHOR_EMAIL=forge@example.com", "GIT_COMMITTER_NAME=forge", "GIT_COMMITTER_EMAIL=forge@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %s", strings.Join(args, " "), out)
		}
	}
	run("init")
	run("add", ".")
	run("commit", "-m", "fixture")
	dest := filepath.Join(t.TempDir(), "out")
	// A path clone has every blob already, so sparse-checkout does not
	// need a second fetch. This exercises applySparseCone itself.
	clone := exec.Command("git", "clone", "--", src, dest)
	clone.Env = gitSafeEnv()
	if out, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("clone: %s", out)
	}
	if err := applySparseCone(context.Background(), gitSafeEnv(), dest, "svc"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "svc", "app.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "other", "skip.txt")); !os.IsNotExist(err) {
		t.Fatalf("sibling remained in the working tree: %v", err)
	}
}

func TestFetchScopeRejectsLocalPath(t *testing.T) {
	err := (HostFetcher{}).FetchScope(context.Background(), `C:\repos\app`, t.TempDir(), "", "", "svc")
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("local path must not be cloned: %v", err)
	}
}
