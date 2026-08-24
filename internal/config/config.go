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

// Config is process configuration for the Forge API.
type Config struct {
	// Addr is host:port for net/http. Default is loopback so a local
	// `go run` does not publish the API on the LAN.
	Addr string

	// DatabaseURL is a PostgreSQL connection string (postgres://...).
	// Required: without a control-plane database the API cannot later
	// persist deployments.
	DatabaseURL string

	// RedisURL is a redis:// URL. Required for increment 0.1 liveness;
	// later phases will use Redis as a queue/cache.
	RedisURL string
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
		Addr:        getenv("FORGE_API_ADDR", "127.0.0.1:8080"),
		DatabaseURL: os.Getenv("FORGE_DATABASE_URL"),
		RedisURL:    os.Getenv("FORGE_REDIS_URL"),
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

// getenv returns the trimmed environment value, or fallback if unset/blank.
// Addr has a safe local default; store URLs do not — guessing a database
// password would hide misconfiguration.
func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
