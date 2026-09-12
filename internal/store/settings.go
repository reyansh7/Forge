package store

import (
	"fmt"
	"regexp"
	"strings"
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
	"docker":    {},
	"forge":     {},
	"localhost": {},
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
