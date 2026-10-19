// Package config loads control-plane settings from the environment.
//
// Forge does not infer API bind addresses or store URLs from a user's
// git repository. Those values are operator configuration. Secrets stay
// in env / .env (gitignored), never in source.
package config

import (
	"fmt"
	"net"
	"net/url"
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

	// WorkspaceMaxBytes caps regular files in one fetched workspace.
	// 0 means DefaultWorkspaceMaxBytes (256 MiB). This is an application
	// quota, not free space on the host disk. Override with
	// FORGE_WORKSPACE_MAX_BYTES.
	WorkspaceMaxBytes int64

	// BuildCache indexes validated images so a later deploy with the
	// same inputs can skip docker build. Default on. FORGE_BUILD_CACHE=off
	// forces a build. The index is not a shared package cache.
	BuildCache bool

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
	MaxInflightPerNode  int
	LogRetentionDays    int
	AllowPublicBind     bool

	// NodeName is which worker this process claims (default "local").
	// NodeToken is the one-time join token except for the reserved local node.
	// NodeAdvertiseHost is how Caddy reaches published ports on this machine.
	NodeName          string
	NodeToken         string
	NodeAdvertiseHost string

	// GitHub OAuth app credentials. Empty means GitHub connect is
	// disabled (503). Tokens for users are stored sealed in Postgres,
	// never in these fields. PublicBase is the dashboard origin used
	// as the OAuth redirect (cookie lives on :3000). WebhookBase is
	// the URL GitHub POSTs to (API or a tunnel).
	GitHubClientID     string
	GitHubClientSecret string
	PublicBase         string
	WebhookBase        string

	// GitHubAllowedUserIDs is the personal-PaaS allowlist. Empty refuses
	// every GitHub login and GitHub connect. A webhook is still trusted
	// only by its HMAC, not by membership in this list.
	GitHubAllowedUserIDs []int64

	// AppsDomain is the zone Forge assigns as {slug}.{zone}, for example
	// forge.com. Empty leaves public names on the 7.6 custom-domain path
	// and keeps Caddy from requesting certificates for platform hosts.
	AppsDomain string

	// ControlHost is the dashboard origin Caddy publishes when AppsDomain
	// is set (console.forge.com). It proxies to DashboardUpstream so
	// GitHub can reach /forge-api/webhooks without publishing :8080.
	ControlHost       string
	DashboardUpstream string
}

const (
	DefaultMaxProjects        = 20
	DefaultMaxInflightDeploys = 2
	DefaultMaxInflightPerNode = 4
	DefaultLogRetentionDays   = 14
	// DefaultWorkspaceMaxBytes is the fetched-workspace cap when
	// FORGE_WORKSPACE_MAX_BYTES is unset: 256 MiB (268435456). The
	// worker compares it with regular-file bytes after clone. A full
	// host disk is a different failure (the clone or write itself).
	DefaultWorkspaceMaxBytes = 256 << 20
	defaultDataKeyFile       = ".forge/data.key"
	DefaultNodeName          = "local"
)

// Load reads FORGE_* environment variables into Config.
func buildCacheEnabled(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "on", "1", "true":
		return true
	case "off", "0", "false":
		return false
	default:
		return true
	}
}

func Load() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}
	cfg, err := common()
	if err != nil {
		return Config{}, err
	}
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
	if err := validatePublicExposure(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadWorker reads operator env for cmd/worker.
func LoadWorker() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}
	cfg, err := common()
	if err != nil {
		return Config{}, err
	}
	if err := requireStores(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func common() (Config, error) {
	allowed, err := parseGitHubAllowedUserIDs(getenv("FORGE_GITHUB_ALLOWED_USER_IDS", ""))
	if err != nil {
		return Config{}, err
	}
	appsDomain, err := parseDNSName("FORGE_APPS_DOMAIN", getenv("FORGE_APPS_DOMAIN", ""))
	if err != nil {
		return Config{}, err
	}
	controlHost, err := parseDNSName("FORGE_CONTROL_HOST", getenv("FORGE_CONTROL_HOST", ""))
	if err != nil {
		return Config{}, err
	}
	upstream, err := parseHostPort("FORGE_DASHBOARD_UPSTREAM", getenv("FORGE_DASHBOARD_UPSTREAM", "host.docker.internal:3000"))
	if err != nil {
		return Config{}, err
	}
	return Config{
		DatabaseURL:          os.Getenv("FORGE_DATABASE_URL"),
		RedisURL:             os.Getenv("FORGE_REDIS_URL"),
		WorkspaceDir:         getenv("FORGE_WORKSPACE_DIR", ""),
		WorkspaceMaxBytes:    getenvInt64("FORGE_WORKSPACE_MAX_BYTES", DefaultWorkspaceMaxBytes),
		BuildCache:           buildCacheEnabled(getenv("FORGE_BUILD_CACHE", "")),
		CaddyAdminURL:        getenv("FORGE_CADDY_ADMIN_URL", "http://127.0.0.1:2019"),
		CaddyUpstreamHost:    getenv("FORGE_CADDY_UPSTREAM_HOST", "host.docker.internal"),
		ProxyPublicBase:      getenv("FORGE_PROXY_PUBLIC_BASE", "http://127.0.0.1:9080"),
		TLSCertFile:          getenv("FORGE_TLS_CERT_FILE", ""),
		TLSKeyFile:           getenv("FORGE_TLS_KEY_FILE", ""),
		DataKey:              getenv("FORGE_DATA_KEY", ""),
		DataKeyFile:          getenv("FORGE_DATA_KEY_FILE", defaultDataKeyFile),
		MaxProjectsPerOwner:  getenvInt("FORGE_MAX_PROJECTS_PER_OWNER", DefaultMaxProjects),
		MaxInflightDeploys:   getenvInt("FORGE_MAX_INFLIGHT_DEPLOYS", DefaultMaxInflightDeploys),
		MaxInflightPerNode:   getenvInt("FORGE_MAX_INFLIGHT_PER_NODE", DefaultMaxInflightPerNode),
		LogRetentionDays:     getenvInt("FORGE_LOG_RETENTION_DAYS", DefaultLogRetentionDays),
		AllowPublicBind:      getenv("FORGE_ALLOW_PUBLIC_BIND", "") == "1",
		NodeName:             getenv("FORGE_NODE_NAME", DefaultNodeName),
		NodeToken:            getenv("FORGE_NODE_TOKEN", ""),
		NodeAdvertiseHost:    getenv("FORGE_NODE_ADVERTISE_HOST", ""),
		GitHubClientID:       getenv("FORGE_GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:   getenv("FORGE_GITHUB_CLIENT_SECRET", ""),
		PublicBase:           getenv("FORGE_PUBLIC_BASE", "http://127.0.0.1:3000"),
		WebhookBase:          getenv("FORGE_WEBHOOK_BASE", "http://127.0.0.1:8080"),
		GitHubAllowedUserIDs: allowed,
		AppsDomain:           appsDomain,
		ControlHost:          controlHost,
		DashboardUpstream:    upstream,
	}, nil
}

// parseGitHubAllowedUserIDs splits a comma-separated list of numeric
// GitHub user ids. Empty means nobody. Names are rejected because a
// login can be renamed or squatted; the numeric id cannot.
func parseGitHubAllowedUserIDs(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []int64
	seen := map[int64]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("FORGE_GITHUB_ALLOWED_USER_IDS: %q is not a GitHub user id", part)
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out, nil
}

func parseDNSName(key, raw string) (string, error) {
	s := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if s == "" {
		return "", nil
	}
	if strings.Contains(s, "*") || net.ParseIP(s) != nil || !strings.Contains(s, ".") {
		return "", fmt.Errorf("%s must be a DNS name like forge.com", key)
	}
	for _, lab := range strings.Split(s, ".") {
		if lab == "" || len(lab) > 63 || lab[0] == '-' || lab[len(lab)-1] == '-' {
			return "", fmt.Errorf("%s is not a valid DNS name", key)
		}
		for _, r := range lab {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return "", fmt.Errorf("%s must be lowercase ASCII", key)
			}
		}
	}
	return s, nil
}

func parseHostPort(key, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	host, port, err := net.SplitHostPort(raw)
	if err != nil || host == "" || port == "" {
		return "", fmt.Errorf("%s must be host:port", key)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("%s port is invalid", key)
	}
	return host + ":" + port, nil
}

// GitHubUserAllowed is the personal-PaaS check. An empty allowlist
// allows nobody, including the account that just finished OAuth.
func (c Config) GitHubUserAllowed(id int64) bool {
	for _, allowed := range c.GitHubAllowedUserIDs {
		if allowed == id {
			return true
		}
	}
	return false
}

// GitHubConfigured is true when the operator registered an OAuth app.
func (c Config) GitHubConfigured() bool {
	return strings.TrimSpace(c.GitHubClientID) != "" && strings.TrimSpace(c.GitHubClientSecret) != ""
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

// validatePublicExposure is the gate before a non-loopback API.
//
// FORGE_ALLOW_PUBLIC_BIND=1 is how an operator would later publish the
// control plane. It is not an AWS deploy. The process refuses to start
// on every interface with the Compose default password or without TLS,
// because that combination is a public control plane with a known secret.
func validatePublicExposure(cfg Config) error {
	if !cfg.AllowPublicBind {
		return nil
	}
	if !cfg.TLSEnabled() {
		return fmt.Errorf("FORGE_ALLOW_PUBLIC_BIND=1 requires FORGE_TLS_CERT_FILE and FORGE_TLS_KEY_FILE")
	}
	u, err := url.Parse(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("FORGE_DATABASE_URL: %w", err)
	}
	pass := ""
	if u.User != nil {
		pass, _ = u.User.Password()
	}
	if pass == "" || pass == "forge" {
		return fmt.Errorf("FORGE_ALLOW_PUBLIC_BIND=1 refuses an empty or default Postgres password; set FORGE_POSTGRES_PASSWORD and FORGE_DATABASE_URL")
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
