package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfineRootStaysInsideClone(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "apps", "web")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := ConfineRoot(base, "apps/web")
	if err != nil {
		t.Fatal(err)
	}
	if got != sub {
		t.Fatalf("got %q want %q", got, sub)
	}
}

func TestConfineRootRejectsEscape(t *testing.T) {
	base := t.TempDir()
	if _, err := ConfineRoot(base, ".."); err == nil {
		t.Fatal("expected escape rejection")
	}
}

func TestConfineRootDefaultIsClone(t *testing.T) {
	base := t.TempDir()
	got, err := ConfineRoot(base, "")
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(base)
	if got != abs {
		t.Fatalf("got %q want %q", got, abs)
	}
}
