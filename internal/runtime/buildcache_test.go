package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCacheKeyIgnoresRuntimeSecrets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := "db-password-should-not-be-keyed"
	base := []EnvPair{{Key: "DATABASE_URL", Value: secret}, {Key: "VITE_API_URL", Value: "/api"}}
	first, err := BuildCacheKey(dir, base)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first, secret) {
		t.Fatal("cache key must not contain the runtime secret")
	}
	changedSecret, err := BuildCacheKey(dir, []EnvPair{{Key: "DATABASE_URL", Value: "other-secret"}, {Key: "VITE_API_URL", Value: "/api"}})
	if err != nil {
		t.Fatal(err)
	}
	if changedSecret != first {
		t.Fatal("a runtime secret change must not invalidate the image key")
	}
	changedPublic, err := BuildCacheKey(dir, []EnvPair{{Key: "DATABASE_URL", Value: secret}, {Key: "VITE_API_URL", Value: "https://api.example"}})
	if err != nil {
		t.Fatal(err)
	}
	if changedPublic == first {
		t.Fatal("a public build value must invalidate the image key")
	}
}

func TestBuildCacheKeyTracksSourceNotCommit(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.txt")
	if err := os.WriteFile(file, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := BuildCacheKey(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := BuildCacheKey(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("a source change must invalidate the image key")
	}
}

func TestBuildCacheKeyDoesNotFollowSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "linked")); err != nil {
		t.Skip("symlink is not permitted on this host: " + err.Error())
	}
	key, err := BuildCacheKey(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(key, "outside") {
		t.Fatal("symlink target contents must not be hashed")
	}
}
