package config

import (
	"os"
	"testing"
)

func TestLoadRequiresStoreURLs(t *testing.T) {
	// Store URLs have no defaults: a silent empty string would look like
	// "configured" until the first real query. Load must fail closed.
	t.Setenv("FORGE_DATABASE_URL", "")
	t.Setenv("FORGE_REDIS_URL", "")
	os.Unsetenv("FORGE_DATABASE_URL")
	os.Unsetenv("FORGE_REDIS_URL")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when store URLs are missing")
	}
}

func TestLoadDefaultsAddrToLoopback(t *testing.T) {
	// Empty FORGE_API_ADDR must become 127.0.0.1:8080, not 0.0.0.0, so a
	// local run does not bind every interface.
	t.Setenv("FORGE_API_ADDR", "")
	t.Setenv("FORGE_DATABASE_URL", "postgres://forge:forge@127.0.0.1:5432/forge?sslmode=disable")
	t.Setenv("FORGE_REDIS_URL", "redis://127.0.0.1:6379/0")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:8080" {
		t.Fatalf("Addr = %q, want loopback :8080", cfg.Addr)
	}
}

func TestLoadHonorsAddrOverride(t *testing.T) {
	t.Setenv("FORGE_API_ADDR", "127.0.0.1:9090")
	t.Setenv("FORGE_DATABASE_URL", "postgres://example")
	t.Setenv("FORGE_REDIS_URL", "redis://example")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:9090" {
		t.Fatalf("Addr = %q", cfg.Addr)
	}
}
