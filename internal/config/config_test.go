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

func TestLoadWorkerRequiresDatabaseAndRedis(t *testing.T) {
	// Deployment status is durable: the worker must open Postgres.
	t.Setenv("FORGE_DATABASE_URL", "")
	os.Unsetenv("FORGE_DATABASE_URL")
	t.Setenv("FORGE_REDIS_URL", "redis://127.0.0.1:6379/0")

	if _, err := LoadWorker(); err == nil {
		t.Fatal("expected error when FORGE_DATABASE_URL is missing")
	}

	t.Setenv("FORGE_DATABASE_URL", "postgres://forge:forge@127.0.0.1:15432/forge?sslmode=disable")
	cfg, err := LoadWorker()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RedisURL != "redis://127.0.0.1:6379/0" {
		t.Fatalf("RedisURL = %q", cfg.RedisURL)
	}
	if cfg.CaddyAdminURL != "http://127.0.0.1:2019" {
		t.Fatalf("CaddyAdminURL = %q", cfg.CaddyAdminURL)
	}
	if cfg.NodeName != DefaultNodeName {
		t.Fatalf("NodeName = %q, want %q", cfg.NodeName, DefaultNodeName)
	}
	if cfg.MaxInflightPerNode != DefaultMaxInflightPerNode {
		t.Fatalf("MaxInflightPerNode = %d", cfg.MaxInflightPerNode)
	}
}

func TestLoadRejectsUnspecifiedBind(t *testing.T) {
	t.Setenv("FORGE_API_ADDR", "0.0.0.0:8080")
	t.Setenv("FORGE_DATABASE_URL", "postgres://example")
	t.Setenv("FORGE_REDIS_URL", "redis://example")
	t.Setenv("FORGE_ALLOW_PUBLIC_BIND", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for 0.0.0.0 without FORGE_ALLOW_PUBLIC_BIND")
	}
}

func TestLoadAllowsPublicBindWhenFlagged(t *testing.T) {
	t.Setenv("FORGE_API_ADDR", "0.0.0.0:8080")
	t.Setenv("FORGE_DATABASE_URL", "postgres://forge:not-the-default@127.0.0.1:15432/forge")
	t.Setenv("FORGE_REDIS_URL", "redis://example")
	t.Setenv("FORGE_ALLOW_PUBLIC_BIND", "1")
	t.Setenv("FORGE_TLS_CERT_FILE", "cert.pem")
	t.Setenv("FORGE_TLS_KEY_FILE", "key.pem")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AllowPublicBind {
		t.Fatal("expected AllowPublicBind")
	}
}

func TestLoadPublicBindRequiresTLSAndPassword(t *testing.T) {
	t.Setenv("FORGE_API_ADDR", "0.0.0.0:8080")
	t.Setenv("FORGE_DATABASE_URL", "postgres://forge:forge@127.0.0.1:15432/forge")
	t.Setenv("FORGE_REDIS_URL", "redis://example")
	t.Setenv("FORGE_ALLOW_PUBLIC_BIND", "1")
	t.Setenv("FORGE_TLS_CERT_FILE", "cert.pem")
	t.Setenv("FORGE_TLS_KEY_FILE", "key.pem")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for the default postgres password")
	}
	t.Setenv("FORGE_DATABASE_URL", "postgres://forge:not-the-default@127.0.0.1:15432/forge")
	t.Setenv("FORGE_TLS_CERT_FILE", "")
	t.Setenv("FORGE_TLS_KEY_FILE", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when public bind has no TLS")
	}
}

func TestLoadTLSPairRequired(t *testing.T) {
	t.Setenv("FORGE_API_ADDR", "127.0.0.1:8080")
	t.Setenv("FORGE_DATABASE_URL", "postgres://example")
	t.Setenv("FORGE_REDIS_URL", "redis://example")
	t.Setenv("FORGE_TLS_CERT_FILE", "cert.pem")
	t.Setenv("FORGE_TLS_KEY_FILE", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when only the cert is set")
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

func TestLoadGitHubDefaultsOff(t *testing.T) {
	t.Setenv("FORGE_API_ADDR", "127.0.0.1:8080")
	t.Setenv("FORGE_DATABASE_URL", "postgres://example")
	t.Setenv("FORGE_REDIS_URL", "redis://example")
	t.Setenv("FORGE_GITHUB_CLIENT_ID", "")
	t.Setenv("FORGE_GITHUB_CLIENT_SECRET", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitHubConfigured() {
		t.Fatal("empty credentials must disable oauth")
	}
	if cfg.PublicBase != "http://127.0.0.1:3000" || cfg.WebhookBase != "http://127.0.0.1:8080" {
		t.Fatalf("bases %+v", cfg)
	}
	t.Setenv("FORGE_GITHUB_CLIENT_ID", "iv")
	t.Setenv("FORGE_GITHUB_CLIENT_SECRET", "sec")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.GitHubConfigured() {
		t.Fatal("expected configured")
	}
}

func TestGitHubAllowlistRejectsNames(t *testing.T) {
	t.Setenv("FORGE_DATABASE_URL", "postgres://forge:forge@127.0.0.1:5432/forge?sslmode=disable")
	t.Setenv("FORGE_REDIS_URL", "redis://127.0.0.1:6379/0")
	t.Setenv("FORGE_GITHUB_ALLOWED_USER_IDS", "octocat")
	if _, err := Load(); err == nil {
		t.Fatal("expected a numeric user id")
	}
	t.Setenv("FORGE_GITHUB_ALLOWED_USER_IDS", "42, 42")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.GitHubUserAllowed(42) || cfg.GitHubUserAllowed(7) {
		t.Fatalf("allowlist %+v", cfg.GitHubAllowedUserIDs)
	}
	t.Setenv("FORGE_APPS_DOMAIN", "*.forge.com")
	if _, err := Load(); err == nil {
		t.Fatal("wildcard apps domain")
	}
}

func TestBuildCacheDefaultsOn(t *testing.T) {
	t.Setenv("FORGE_API_ADDR", "")
	t.Setenv("FORGE_DATABASE_URL", "postgres://forge:forge@127.0.0.1:5432/forge?sslmode=disable")
	t.Setenv("FORGE_REDIS_URL", "redis://127.0.0.1:6379/0")
	t.Setenv("FORGE_BUILD_CACHE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.BuildCache {
		t.Fatal("empty FORGE_BUILD_CACHE must leave the image index on")
	}
	t.Setenv("FORGE_BUILD_CACHE", "off")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BuildCache {
		t.Fatal("FORGE_BUILD_CACHE=off must force a build")
	}
}
