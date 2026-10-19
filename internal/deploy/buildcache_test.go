package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reyansh7/Forge/internal/runtime"
	"github.com/reyansh7/Forge/internal/store"
)

const testImageID = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type memCache struct {
	rows map[string][2]string
}

func (m *memCache) key(app, cacheKey string) string { return app + "|" + cacheKey }

func (m *memCache) LookupBuildCache(_ context.Context, applicationID, cacheKey string) (string, string, bool, error) {
	if m.rows == nil {
		return "", "", false, nil
	}
	row, ok := m.rows[m.key(applicationID, cacheKey)]
	if !ok {
		return "", "", false, nil
	}
	return row[0], row[1], true, nil
}

func (m *memCache) RememberBuildCache(_ context.Context, applicationID, cacheKey, imageName, imageID string) error {
	if m.rows == nil {
		m.rows = map[string][2]string{}
	}
	k := m.key(applicationID, cacheKey)
	if _, exists := m.rows[k]; exists {
		return nil
	}
	m.rows[k] = [2]string{imageName, imageID}
	return nil
}

func (m *memCache) ForgetBuildCache(_ context.Context, applicationID, cacheKey string) error {
	delete(m.rows, m.key(applicationID, cacheKey))
	return nil
}

type cacheBuilder struct {
	builds int
	fail   bool
	images map[string]string
}

func (b *cacheBuilder) Build(_ context.Context, _, image, _ string, _ []runtime.EnvPair) (string, error) {
	b.builds++
	if b.fail {
		return "build failed\n", errBuild
	}
	if b.images == nil {
		b.images = map[string]string{}
	}
	b.images[image] = testImageID
	return "built\n", nil
}

var errBuild = errString("docker build failed")

type errString string

func (e errString) Error() string { return string(e) }

func (b *cacheBuilder) ImageID(_ context.Context, image string) (string, error) {
	id, ok := b.images[image]
	if !ok {
		return "", errString("missing image")
	}
	return id, nil
}

func (b *cacheBuilder) Tag(_ context.Context, source, dest string) error {
	id, ok := b.images[source]
	if !ok {
		return errString("missing source")
	}
	if b.images == nil {
		b.images = map[string]string{}
	}
	b.images[dest] = id
	return nil
}

func TestBuildCacheReusesImageAndSkipsSecondBuild(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	builder := &cacheBuilder{}
	cache := &memCache{}
	h := Handler{Builder: builder, BuildCache: cache}
	app := store.Application{ID: "11111111-1111-1111-1111-111111111111"}
	env := []runtime.EnvPair{{Key: "DATABASE_URL", Value: "postgres://secret"}, {Key: "VITE_API_URL", Value: "/api"}}
	log, err, label := h.buildWithCache(context.Background(), app, dir, "forge-app-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "image", env)
	if err != nil || label != "miss" || builder.builds != 1 {
		t.Fatalf("first build log=%q err=%v label=%s builds=%d", log, err, label, builder.builds)
	}
	if strings.Contains(log, "postgres://secret") {
		t.Fatal("build log must not contain the runtime secret")
	}
	log, err, label = h.buildWithCache(context.Background(), app, dir, "forge-app-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "image", env)
	if err != nil || label != "hit" || builder.builds != 1 {
		t.Fatalf("second build log=%q err=%v label=%s builds=%d", log, err, label, builder.builds)
	}
	if !strings.Contains(log, "build cache hit") || !strings.Contains(log, "Health checks still run") {
		t.Fatalf("hit log: %s", log)
	}
	id, ok := builder.images["forge-app-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"]
	if !ok || id != testImageID {
		t.Fatalf("retagged image = %q ok=%v", id, ok)
	}
}

func TestBuildCacheMissesWhenPublicValueChanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	builder := &cacheBuilder{}
	h := Handler{Builder: builder, BuildCache: &memCache{}}
	app := store.Application{ID: "22222222-2222-2222-2222-222222222222"}
	imageA := "forge-app-cccccccccccccccccccccccccccccccc"
	imageB := "forge-app-dddddddddddddddddddddddddddddddd"
	if _, err, label := h.buildWithCache(context.Background(), app, dir, imageA, "static", []runtime.EnvPair{{Key: "VITE_API_URL", Value: "/api"}}); err != nil || label != "miss" {
		t.Fatal(err, label)
	}
	if _, err, label := h.buildWithCache(context.Background(), app, dir, imageB, "static", []runtime.EnvPair{{Key: "VITE_API_URL", Value: "https://other.example"}}); err != nil || label != "miss" || builder.builds != 2 {
		t.Fatalf("public change must rebuild: err=%v label=%s builds=%d", err, label, builder.builds)
	}
}

func TestBuildCacheDoesNotRememberAFailedBuild(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	builder := &cacheBuilder{fail: true}
	cache := &memCache{}
	h := Handler{Builder: builder, BuildCache: cache}
	app := store.Application{ID: "33333333-3333-3333-3333-333333333333"}
	_, err, label := h.buildWithCache(context.Background(), app, dir, "forge-app-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "image", nil)
	if err == nil || label != "miss" || len(cache.rows) != 0 {
		t.Fatalf("failed build remembered: err=%v label=%s rows=%d", err, label, len(cache.rows))
	}
}

func TestBuildCacheRejectsMismatchedImageID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := &memCache{}
	app := store.Application{ID: "44444444-4444-4444-4444-444444444444"}
	first := &cacheBuilder{}
	h := Handler{Builder: first, BuildCache: cache}
	if _, err, label := h.buildWithCache(context.Background(), app, dir, "forge-app-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "image", nil); err != nil || label != "miss" {
		t.Fatal(err, label)
	}
	for k, row := range cache.rows {
		row[1] = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		cache.rows[k] = row
	}
	// Inspect still returns the real ID. The index claims a different one.
	next := &cacheBuilder{images: map[string]string{
		"forge-app-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": testImageID,
	}}
	h.Builder = next
	_, err, label := h.buildWithCache(context.Background(), app, dir, "forge-app-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "image", nil)
	if err != nil || label != "miss" || next.builds != 1 {
		t.Fatalf("mismatched id must rebuild: err=%v label=%s builds=%d", err, label, next.builds)
	}
}
