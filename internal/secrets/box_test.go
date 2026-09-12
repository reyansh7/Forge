package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	b, err := New(bytes32('a'))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Seal("DATABASE_URL=secret")
	if err != nil {
		t.Fatal(err)
	}
	if sealed == "DATABASE_URL=secret" || !Sealed(sealed) {
		t.Fatalf("expected ciphertext, got %q", sealed)
	}
	plain, err := b.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "DATABASE_URL=secret" {
		t.Fatalf("plain = %q", plain)
	}
}

func TestOpenLegacyPlaintext(t *testing.T) {
	b, err := New(bytes32('b'))
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Open("already-plain")
	if err != nil {
		t.Fatal(err)
	}
	if got != "already-plain" {
		t.Fatalf("got %q", got)
	}
}

func TestWrongKeyFails(t *testing.T) {
	a, err := New(bytes32('a'))
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(bytes32('z'))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := a.Seal("x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("expected open to fail with another key")
	}
}

func TestLoadOrCreatePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.key")
	first, err := LoadOrCreate(path, "")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := first.Seal("v")
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(path, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := second.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v" {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func bytes32(b byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = b
	}
	return out
}
