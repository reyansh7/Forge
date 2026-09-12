// Package config loads control-plane settings from the environment.
//
// Forge does not infer API bind addresses or store URLs from a user's
// git repository. Those values are operator configuration. Secrets stay
// in env / .env (gitignored), never in source.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
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

	// WorkspaceMaxBytes caps a fetched repo tree. 0 means the default.
	WorkspaceMaxBytes int64

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

	// TLSCertFile / TLSKeyFile enable ListenAndServeTLS. Both or neither.
	TLSCertFile string
	TLSKeyFile  string

	// DataKey is hex/base64 of the 32-byte env-at-rest key. Empty means
	// load or create DataKeyFile.
	DataKey     string
	DataKeyFile string

	MaxProjectsPerOwner int
	MaxInflightDeploys  int
	LogRetentionDays    int
	AllowPublicBind     bool
}

const (
	DefaultMaxProjects        = 20
	DefaultMaxInflightDeploys = 2
	DefaultLogRetentionDays   = 14
	DefaultWorkspaceMaxBytes  = 256 << 20
	defaultDataKeyFile        = ".forge/data.key"
)

// Load reads FORGE_* environment variables into Config.
func Load() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}
	cfg := common()
	cfg.Addr = getenv("FORGE_API_ADDR", "127.0.0.1:8080")
	if err := requireStores(&cfg); err != nil {
		return Config{}, err
	}
	if err := validateTLS(cfg); err != nil {
		return Config{}, err
	}
	if err := validateBind(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadWorker reads operator env for cmd/worker.
func LoadWorker() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}
	cfg := common()
	if err := requireStores(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func common() Config {
	return Config{
		DatabaseURL:         os.Getenv("FORGE_DATABASE_URL"),
		RedisURL:            os.Getenv("FORGE_REDIS_URL"),
		WorkspaceDir:        getenv("FORGE_WORKSPACE_DIR", ""),
		WorkspaceMaxBytes:   getenvInt64("FORGE_WORKSPACE_MAX_BYTES", DefaultWorkspaceMaxBytes),
		CaddyAdminURL:       getenv("FORGE_CADDY_ADMIN_URL", "http://127.0.0.1:2019"),
		CaddyUpstreamHost:   getenv("FORGE_CADDY_UPSTREAM_HOST", "host.docker.internal"),
		ProxyPublicBase:     getenv("FORGE_PROXY_PUBLIC_BASE", "http://127.0.0.1:9080"),
		TLSCertFile:         getenv("FORGE_TLS_CERT_FILE", ""),
		TLSKeyFile:          getenv("FORGE_TLS_KEY_FILE", ""),
		DataKey:             getenv("FORGE_DATA_KEY", ""),
		DataKeyFile:         getenv("FORGE_DATA_KEY_FILE", defaultDataKeyFile),
		MaxProjectsPerOwner: getenvInt("FORGE_MAX_PROJECTS_PER_OWNER", DefaultMaxProjects),
		MaxInflightDeploys:  getenvInt("FORGE_MAX_INFLIGHT_DEPLOYS", DefaultMaxInflightDeploys),
		LogRetentionDays:    getenvInt("FORGE_LOG_RETENTION_DAYS", DefaultLogRetentionDays),
		AllowPublicBind:     getenv("FORGE_ALLOW_PUBLIC_BIND", "") == "1",
	}
}

func requireStores(cfg *Config) error {
	var missing []string
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		missing = append(missing, "FORGE_DATABASE_URL")
	}
	if strings.TrimSpace(cfg.RedisURL) == "" {
		missing = append(missing, "FORGE_REDIS_URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return nil
}

func validateTLS(cfg Config) error {
	cert := strings.TrimSpace(cfg.TLSCertFile)
	key := strings.TrimSpace(cfg.TLSKeyFile)
	if (cert == "") != (key == "") {
		return fmt.Errorf("FORGE_TLS_CERT_FILE and FORGE_TLS_KEY_FILE must be set together")
	}
	return nil
}

func validateBind(cfg Config) error {
	if cfg.AllowPublicBind {
		return nil
	}
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return fmt.Errorf("FORGE_API_ADDR: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return fmt.Errorf("FORGE_API_ADDR %q binds every interface; keep loopback or set FORGE_ALLOW_PUBLIC_BIND=1 (auth is still required; TLS is recommended)", cfg.Addr)
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return fmt.Errorf("FORGE_API_ADDR %q binds every interface; keep loopback or set FORGE_ALLOW_PUBLIC_BIND=1", cfg.Addr)
	}
	return nil
}

// TLSEnabled is true when the API will call ListenAndServeTLS.
func (c Config) TLSEnabled() bool {
	return strings.TrimSpace(c.TLSCertFile) != "" && strings.TrimSpace(c.TLSKeyFile) != ""
}

// getenv returns the trimmed environment value, or fallback if unset/blank.
func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

func getenvInt64(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1 {
		return fallback
	}
	return n
}
