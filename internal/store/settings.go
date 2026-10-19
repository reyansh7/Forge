package store

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Application settings are operator metadata the worker reads on deploy.
//
// They are not a build DSL and not user-supplied shell. The API validates
// here so a bad PATCH is 400 before Postgres. The worker still confines
// root_directory under the clone (defense in depth: a corrupted row must
// not escape the fetch tree).

const (
	maxRootDirectoryLen = 200
	maxHealthPathLen    = 200
	maxLocalHostLen     = 63
	maxBuildLogLen      = 32768
)

// localHostPattern is a single DNS label. It is interpolated into a Caddy
// Host matcher, so it must not contain regex metacharacters or dots
// (dots would make "foo.localhost" a two-label name we do not own).
var localHostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// reservedLocalHosts would collide with the control plane or OS names.
// "localhost" itself is the suffix we append; claiming it as a slug would
// produce localhost.localhost and confuse operators.
var reservedLocalHosts = map[string]struct{}{
	"admin":     {},
	"api":       {},
	"caddy":     {},
	"cdn":       {},
	"console":   {},
	"dashboard": {},
	"docker":    {},
	"forge":     {},
	"git":       {},
	"hooks":     {},
	"localhost": {},
	"login":     {},
	"mail":      {},
	"observe":   {},
	"ssh":       {},
	"status":    {},
	"www":       {},
}

// ValidateRootDirectory returns a relative POSIX-ish path for detect/build.
//
// Empty becomes "." (the clone root). Backslashes are rejected so Windows
// operators cannot smuggle a drive path. ".." is rejected so the value
// cannot climb out of the clone even before ConfineRoot runs.
func ValidateRootDirectory(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		s = "."
	}
	if utf8.RuneCountInString(s) > maxRootDirectoryLen {
		return "", fmt.Errorf("root_directory must be at most %d characters", maxRootDirectoryLen)
	}
	if strings.ContainsAny(s, `\:`) || strings.Contains(s, "\x00") {
		return "", fmt.Errorf("root_directory must be a relative path")
	}
	if strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("root_directory must be a relative path")
	}
	parts := strings.Split(s, "/")
	for _, p := range parts {
		if p == ".." {
			return "", fmt.Errorf("root_directory must not contain ..")
		}
		if p == "" && s != "." {
			return "", fmt.Errorf("root_directory must not contain empty segments")
		}
	}
	return s, nil
}

// ValidateHealthPath is the HTTP path appended to http://127.0.0.1:{port}.
//
// It must start with "/" so it cannot be interpreted as a host. "://" is
// rejected so "http://169.254.169.254/" cannot become an SSRF target — the
// probe URL is always constructed as loopback + this path.
func ValidateHealthPath(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		s = "/"
	}
	if utf8.RuneCountInString(s) > maxHealthPathLen {
		return "", fmt.Errorf("health_path must be at most %d characters", maxHealthPathLen)
	}
	if !strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("health_path must start with /")
	}
	if strings.Contains(s, "://") || strings.ContainsAny(s, "\x00\r\n \\") {
		return "", fmt.Errorf("health_path is not a valid path")
	}
	return s, nil
}

// ValidateLocalHost is an optional .localhost slug (not a public domain).
//
// Empty means "path-only routing" (/d/{id}/). A set value is unique across
// applications. Reserved names are rejected so an app cannot steal Host
// headers the operator might use for Caddy admin or the dashboard.
func ValidateLocalHost(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", nil
	}
	if utf8.RuneCountInString(s) > maxLocalHostLen {
		return "", fmt.Errorf("local_host must be at most %d characters", maxLocalHostLen)
	}
	if !localHostPattern.MatchString(s) {
		return "", fmt.Errorf("local_host must be a lowercase DNS label")
	}
	if _, reserved := reservedLocalHosts[s]; reserved {
		return "", fmt.Errorf("local_host %q is reserved", s)
	}
	// Preview Hosts are {slug}-preview.localhost. An operator slug
	// with that suffix would steal another app's preview matcher.
	if strings.HasSuffix(s, previewHostSuffix) {
		return "", fmt.Errorf("local_host must not end with %s", previewHostSuffix)
	}
	return s, nil
}

// SuggestedLocalHost is the Host slug Forge uses when the operator
// left local_host empty.
//
// A slug is not required to deploy. Path URLs (/d/{id}/) still exist,
// but SPAs that request /assets/* from the origin root only work
// reliably on a Host mounted at /. Default application name "app"
// becomes app-{short-id} so two unnamed apps do not share app.localhost.
func SuggestedLocalHost(name, applicationID string) string {
	slug := slugifyLocalHost(name)
	short := shortHostLabel(applicationID)
	if slug == "" || slug == "app" {
		slug = "app-" + short
	}
	if _, reserved := reservedLocalHosts[slug]; reserved {
		slug = slug + "-" + short
	}
	got, err := ValidateLocalHost(slug)
	if err != nil || got == "" {
		return "app-" + short
	}
	return got
}

// BridgeAlias is the DNS name other containers on the project bridge use.
//
// Public Host slugs reject reserved labels such as "api" so an
// application cannot take api.localhost. A Compose file can still
// hard-code http://api:5000 inside an image. That name is only a
// bridge alias, not a public hostname, so the reserved-label rule
// does not apply here. An operator local_host replaces the alias.
func BridgeAlias(name, localHost, applicationID string) string {
	if slug := strings.ToLower(strings.TrimSpace(localHost)); slug != "" {
		return slug
	}
	slug := slugifyLocalHost(name)
	if slug == "" || slug == "host" || slug == "bridge" || slug == "none" {
		return SuggestedLocalHost(name, applicationID)
	}
	if !localHostPattern.MatchString(slug) {
		return SuggestedLocalHost(name, applicationID)
	}
	return slug
}

func slugifyLocalHost(name string) string {
	var b strings.Builder
	lastDash := true
	n := 0
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
			n++
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
				n++
			}
		}
		if n >= 40 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func shortHostLabel(id string) string {
	hex := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(id), "-", ""))
	if len(hex) < 4 {
		return "0000"
	}
	for _, r := range hex[:4] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "0000"
		}
	}
	return hex[:4]
}

// ValidateApplication is name + git URL + Phase 2 settings in one pass.
// Create/Update call this so a worker cannot insert a row the HTTP layer
// would have rejected.
func ValidateApplication(name, repositoryURL, rootDirectory, healthPath, localHost string) (ApplicationInput, error) {
	in, err := ValidateApplicationInput(name, repositoryURL)
	if err != nil {
		return ApplicationInput{}, err
	}
	root, err := ValidateRootDirectory(rootDirectory)
	if err != nil {
		return ApplicationInput{}, err
	}
	health, err := ValidateHealthPath(healthPath)
	if err != nil {
		return ApplicationInput{}, err
	}
	host, err := ValidateLocalHost(localHost)
	if err != nil {
		return ApplicationInput{}, err
	}
	in.RootDirectory = root
	in.HealthPath = health
	in.LocalHost = host
	return in, nil
}

// ValidateResources checks the container budget and the readiness
// timeout. Zero keeps the historical default. The same ceilings are
// CHECK constraints in migration 0021.
func ValidateResources(memoryMB, cpuMillis, pids, healthTimeoutSeconds int) error {
	if memoryMB != 0 && (memoryMB < 64 || memoryMB > 2048) {
		return fmt.Errorf("memory_mb must be 0 or between 64 and 2048")
	}
	if cpuMillis != 0 && (cpuMillis < 100 || cpuMillis > 2000) {
		return fmt.Errorf("cpu_millis must be 0 or between 100 and 2000")
	}
	if pids != 0 && (pids < 32 || pids > 1024) {
		return fmt.Errorf("pids_limit must be 0 or between 32 and 1024")
	}
	if healthTimeoutSeconds != 0 && (healthTimeoutSeconds < 5 || healthTimeoutSeconds > 180) {
		return fmt.Errorf("health_timeout_seconds must be 0 or between 5 and 180")
	}
	return nil
}

// ResolveHealthTimeout turns the stored seconds into the probe budget.
// Zero uses fallback, which itself defaults to 45s and is clamped to
// the same 5–180s window so a misconfigured process cannot wait forever.
// ValidateExposure accepts the manifest's public/private flag.
// Empty means the historical role default.
func ValidateExposure(raw string) error {
	switch strings.TrimSpace(raw) {
	case "", "public", "private":
		return nil
	default:
		return fmt.Errorf("exposure must be public, private, or empty")
	}
}

func ResolveHealthTimeout(seconds int, fallback time.Duration) (time.Duration, error) {
	if err := ValidateResources(0, 0, 0, seconds); err != nil {
		return 0, err
	}
	if seconds == 0 {
		if fallback < 5*time.Second || fallback > 180*time.Second {
			fallback = 45 * time.Second
		}
		return fallback, nil
	}
	return time.Duration(seconds) * time.Second, nil
}

// ValidateBuildPackage accepts the Go package a deploy should compile
// from the module root. Empty and "." mean "let detect choose".
// ./cmd/<name> is the only other form: it cannot point outside the
// module, and it is not a shell command.
func ValidateBuildPackage(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "." {
		return s, nil
	}
	name, ok := strings.CutPrefix(s, "./cmd/")
	if !ok || strings.Contains(name, "/") || !buildPackageName(name) {
		return "", fmt.Errorf("build_package must be empty, ., or ./cmd/<name>")
	}
	return "./cmd/" + name, nil
}

func buildPackageName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// SanitizeBuildLog caps docker build output for Postgres and the UI.
//
// Keep the tail: build failures are usually the last lines. Strip NUL so
// the value cannot split logs or JSON. Do not log this from the API; the
// dashboard fetches it on GET /deployments/{id}.
func SanitizeBuildLog(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.ReplaceAll(s, "\x00", "")
	if len(s) <= maxBuildLogLen {
		return s
	}
	return s[len(s)-maxBuildLogLen:]
}
