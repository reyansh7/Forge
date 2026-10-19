package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reyansh7/Forge/internal/store"
)

func TestHostFetcherCopiesHello(t *testing.T) {
	dest := t.TempDir()
	if err := (HostFetcher{}).Fetch(context.Background(), store.SampleHelloURL, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "server.py")); err != nil {
		t.Fatal(err)
	}
}

func TestHostFetcherCopiesHelloForGitHubExamplePlaceholder(t *testing.T) {
	// github.com/example is not a real org; Phase 0 maps it to the sample
	// so a tutorial URL does not fail at git clone.
	dest := t.TempDir()
	err := (HostFetcher{}).Fetch(context.Background(), "https://github.com/example/forge.git", dest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "server.py")); err != nil {
		t.Fatal(err)
	}
}

func TestRefuseIncompleteCheckout(t *testing.T) {
	dir := t.TempDir()
	if err := refuseIncompleteCheckout(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitmodules"), []byte("[submodule \"libs/model\"]\n\tpath = libs/model\n\turl = https://example.com/model.git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refuseIncompleteCheckout(dir); err == nil || !strings.Contains(err.Error(), ".gitmodules") {
		t.Fatalf("submodule err = %v", err)
	}
	if err := os.Remove(filepath.Join(dir, ".gitmodules")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.joblib filter=lfs diff=lfs merge=lfs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refuseIncompleteCheckout(dir); err == nil || !strings.Contains(err.Error(), "Git LFS") {
		t.Fatalf("lfs err = %v", err)
	}
}

func TestRefuseIncompleteCheckoutSkipsSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("[submodule \"x\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".gitmodules")); err != nil {
		t.Skip("symlink: " + err.Error())
	}
	err := refuseIncompleteCheckout(dir)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v", err)
	}
}

func TestHostFetcherRejectsFileURL(t *testing.T) {
	err := (HostFetcher{}).Fetch(context.Background(), "file:///etc/passwd", t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHostFetcherRejectsUserinfo(t *testing.T) {
	err := (HostFetcher{}).Fetch(context.Background(), "https://user:token@example.com/repo.git", t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGitAskedForCredentials(t *testing.T) {
	if !gitAskedForCredentials("fatal: could not read Username for 'https://github.com': terminal prompts disabled") {
		t.Fatal("username prompt")
	}
	if !gitAskedForCredentials("fatal: could not read Password for 'https://github.com': terminal prompts disabled") {
		t.Fatal("password prompt")
	}
	if gitAskedForCredentials("fatal: repository not found") {
		t.Fatal("missing repo is not a credential prompt")
	}
}

func TestHostFetcherFetchRefRejectsUserinfo(t *testing.T) {
	err := (HostFetcher{}).FetchRef(context.Background(), "https://user:token@example.com/repo.git", t.TempDir(), "abc1234", "gho_x")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHostFetcherFetchRefCopiesHello(t *testing.T) {
	dest := t.TempDir()
	if err := (HostFetcher{}).FetchRef(context.Background(), store.SampleHelloURL, dest, "abc1234", "gho_x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "server.py")); err != nil {
		t.Fatal(err)
	}
}

func TestHostFetcherRejectsLoopback(t *testing.T) {
	err := (HostFetcher{}).Fetch(context.Background(), "https://127.0.0.1/repo.git", t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateImageName(t *testing.T) {
	if err := validateImageName("forge-app-abc"); err != nil {
		t.Fatal(err)
	}
	if err := validateImageName("bad/name"); err == nil {
		t.Fatal("expected error")
	}
}
