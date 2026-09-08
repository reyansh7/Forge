// Package config loads control-plane settings from the environment.
//
// Forge does not infer API bind addresses or store URLs from a user's
// git repository. Those values are operator configuration. Secrets stay
// in env / .env (gitignored), never in source.
package config

import (
	"fmt"
	"os"
	"strings"
)

// Config is process configuration for the Forge API and worker.
type Config struct {
	// Addr is host:port for net/http. Default is loopback so a local
	// `go run` does not publish the API on the LAN.
	Addr string

	// DatabaseURL is a PostgreSQL connection string (postgres://...).
	// Required for the API and, from increment 0.4, the worker: deployment
	// status is durable state and must not live only on the Redis LIST.
	DatabaseURL string

	// RedisURL is a redis:// URL. Required for /health and for the
	// job LIST. Redis is not the durable project or deployment store.
	RedisURL string

	// WorkspaceDir is where the worker writes ephemeral clone directories.
	// Each deploy gets a subdirectory that is removed after the job.
	WorkspaceDir string

	// CaddyAdminURL is the loopback Caddy admin API (POST /load).
	// The worker updates routes here; it does not exec inside Caddy.
	CaddyAdminURL string

	// CaddyUpstreamHost is how Caddy (in Compose) reaches published
	// app ports on the Docker host. Docker Desktop provides
	// host.docker.internal; a host-run Caddy would use 127.0.0.1.
	CaddyUpstreamHost string

	// ProxyPublicBase is the URL prefix clients use to hit Caddy
	// (http://127.0.0.1:9080). Per-deployment paths are /d/{id}/.
	ProxyPublicBase string
}

// Load reads FORGE_* environment variables into Config.
//
// A `.env` file in the current working directory is applied first, but
// only for keys that are not already set. Docker Compose interpolates
// `.env` automatically; a plain `go run` does not, so we load it here
// without adding a third-party dotenv library.
func Load() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}
	cfg := Config{
		Addr:              getenv("FORGE_API_ADDR", "127.0.0.1:8080"),
		DatabaseURL:       os.Getenv("FORGE_DATABASE_URL"),
		RedisURL:          os.Getenv("FORGE_REDIS_URL"),
		WorkspaceDir:      getenv("FORGE_WORKSPACE_DIR", ""),
		CaddyAdminURL:     getenv("FORGE_CADDY_ADMIN_URL", "http://127.0.0.1:2019"),
		CaddyUpstreamHost: getenv("FORGE_CADDY_UPSTREAM_HOST", "host.docker.internal"),
		ProxyPublicBase:   getenv("FORGE_PROXY_PUBLIC_BASE", "http://127.0.0.1:9080"),
	}

	// Collect missing names so the operator sees every gap in one error,
	// not a whack-a-mole of one variable at a time.
	var missing []string
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		missing = append(missing, "FORGE_DATABASE_URL")
	}
	if strings.TrimSpace(cfg.RedisURL) == "" {
		missing = append(missing, "FORGE_REDIS_URL")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

// LoadWorker reads operator env for cmd/worker.
//
// The worker needs Redis (job transport) and Postgres (deployment rows).
// The LIST is still transient: a Redis restart can drop queued jobs, but
// in-flight/completed status lives in SQL.
func LoadWorker() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}
	cfg := Config{
		DatabaseURL:       os.Getenv("FORGE_DATABASE_URL"),
		RedisURL:          os.Getenv("FORGE_REDIS_URL"),
		WorkspaceDir:      getenv("FORGE_WORKSPACE_DIR", ""),
		CaddyAdminURL:     getenv("FORGE_CADDY_ADMIN_URL", "http://127.0.0.1:2019"),
		CaddyUpstreamHost: getenv("FORGE_CADDY_UPSTREAM_HOST", "host.docker.internal"),
		ProxyPublicBase:   getenv("FORGE_PROXY_PUBLIC_BASE", "http://127.0.0.1:9080"),
	}
	var missing []string
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		missing = append(missing, "FORGE_DATABASE_URL")
	}
	if strings.TrimSpace(cfg.RedisURL) == "" {
		missing = append(missing, "FORGE_REDIS_URL")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

// getenv returns the trimmed environment value, or fallback if unset/blank.
// Addr has a safe local default; store URLs do not — guessing a database
// password would hide misconfiguration.
func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
