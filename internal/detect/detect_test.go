package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTreePrefersDockerfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\n")
	write(t, filepath.Join(dir, "package.json"), `{}`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindDockerfile {
		t.Fatalf("kind = %q", got.Kind)
	}
}

func TestTreeDetectsGo(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindGo {
		t.Fatalf("kind = %q", got.Kind)
	}
}

func TestTreeDetectsNode(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"name":"x"}`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindNode {
		t.Fatalf("kind = %q", got.Kind)
	}
}

func TestTreeRejectsEmptyTree(t *testing.T) {
	if _, err := Tree(t.TempDir()); err == nil {
		t.Fatal("expected error")
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
