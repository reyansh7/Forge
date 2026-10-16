package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"unicode/utf8"
)

// ApplicationDomain is a public hostname the operator wants Caddy to
// serve for one application environment.
//
// What it is: custom DNS, not the .localhost slug. Why: a real URL
// (app.example.com) is how operators share an app; ACME is how that
// URL gets HTTPS without copying certificates into git. How: HTTP
// persists a pending row with a TXT token; after DNS proves ownership
// the worker/API include the hostname in the Caddyfile and Caddy
// obtains/renews a Let's Encrypt cert. .localhost routes are unchanged.
//
// Security: hostname is unique across every application. Listing is
// scoped by application_id so id-guessing cannot attach another app's
// domain. Hostnames are validated before insert and again before they
// are interpolated into a Caddyfile. ACME is not on-demand (anyone
// pointing DNS at this box would otherwise mint unlimited certs).
//
// Deferred: wildcards, www aliases, DNS-01, multiple SANs, GitHub.
type ApplicationDomain struct {
	ID            string
	ApplicationID string
	Hostname      string
	Environment   string
	Status        string
	Token         string
	// Origin is "custom" (operator DNS TXT) or "platform" (Forge reserved
	// {slug}.{apps domain}). Empty is treated as custom by callers that
	// build a row without the column.
	Origin string
}

const (
	DomainPending         = "pending"
	DomainActive          = "active"
	DomainOriginCustom    = "custom"
	DomainOriginPlatform  = "platform"
	maxApplicationDomains = 8
	domainTokenBytes      = 16
)

const domainReturning = `id::text, application_id::text, hostname, environment, status, token, origin`

func scanDomain(sc interface{ Scan(dest ...any) error }) (ApplicationDomain, error) {
	var d ApplicationDomain
	err := sc.Scan(&d.ID, &d.ApplicationID, &d.Hostname, &d.Environment, &d.Status, &d.Token, &d.Origin)
	return d, err
}

// DomainChallengeName is the TXT owner-check CNAME-less target.
//
// `_forge-challenge.<hostname>` cannot collide with a typical WWW
// record. The value is Token. HTTP-01 is Caddy/ACME after activation;
// this TXT is Forge's proof before we even add a Caddy Host.
func DomainChallengeName(hostname string) string {
	h, err := ValidateHostname(hostname)
	if err != nil {
		return ""
	}
	return "_forge-challenge." + h
}

// ValidateHostname is a public DNS name, never a URL, IP, or .localhost.
func ValidateHostname(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		return "", fmt.Errorf("hostname is required")
	}
	if utf8.RuneCountInString(s) > 253 {
		return "", fmt.Errorf("hostname must be at most 253 characters")
	}
	if strings.ContainsAny(s, "/:\\@?#[]% \"'`{}()|,;<>") || strings.Contains(s, "..") {
		return "", fmt.Errorf("hostname is not a valid DNS name")
	}
	if strings.Contains(s, "*") {
		return "", fmt.Errorf("wildcard hostnames are not supported")
	}
	if net.ParseIP(s) != nil {
		return "", fmt.Errorf("hostname must not be an IP address")
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("hostname must be a fully qualified domain name")
	}
	for i, lab := range labels {
		if lab == "" || len(lab) > 63 {
			return "", fmt.Errorf("hostname is not a valid DNS name")
		}
		if lab[0] == '-' || lab[len(lab)-1] == '-' {
			return "", fmt.Errorf("hostname is not a valid DNS name")
		}
		for _, r := range lab {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return "", fmt.Errorf("hostname must be lowercase ASCII")
			}
		}
		if i == len(labels)-1 && len(lab) < 2 {
			return "", fmt.Errorf("hostname TLD is invalid")
		}
	}
	if reservedPublicHostname(s) {
		return "", fmt.Errorf("hostname %q is reserved", s)
	}
	return s, nil
}

func reservedPublicHostname(s string) bool {
	if s == "localhost" {
		return true
	}
	suffixes := []string{
		".localhost", ".local", ".internal", ".arpa",
		".invalid", ".onion", ".localdomain",
	}
	for _, suf := range suffixes {
		if s == strings.TrimPrefix(suf, ".") || strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

func newDomainToken() (string, error) {
	var b [domainTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("domain token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func (p *Postgres) CreateApplicationDomain(ctx context.Context, applicationID, hostname, environment string) (ApplicationDomain, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return ApplicationDomain{}, err
	}
	hostname, err = ValidateHostname(hostname)
	if err != nil {
		return ApplicationDomain{}, err
	}
	environment, err = ParseEnvironment(environment)
	if err != nil {
		return ApplicationDomain{}, err
	}
	if _, err := p.GetApplication(ctx, applicationID); err != nil {
		return ApplicationDomain{}, err
	}
	existing, err := p.ListApplicationDomains(ctx, applicationID)
	if err != nil {
		return ApplicationDomain{}, err
	}
	custom := 0
	for _, row := range existing {
		if row.Origin != DomainOriginPlatform {
			custom++
		}
	}
	if custom >= maxApplicationDomains {
		return ApplicationDomain{}, fmt.Errorf("at most %d custom domains per application", maxApplicationDomains)
	}
	token, err := newDomainToken()
	if err != nil {
		return ApplicationDomain{}, err
	}

	row := p.db.QueryRowContext(ctx, `
		INSERT INTO application_domains (application_id, hostname, environment, status, token, origin)
		VALUES ($1::uuid, $2, $3, $4, $5, $6)
		RETURNING `+domainReturning+`
	`, applicationID, hostname, environment, DomainPending, token, DomainOriginCustom)
	out, err := scanDomain(row)
	if err != nil {
		if isUniqueViolation(err) {
			return ApplicationDomain{}, fmt.Errorf("%w: domain is already claimed", ErrConflict)
		}
		return ApplicationDomain{}, fmt.Errorf("create application domain: %w", err)
	}
	return out, nil
}

func (p *Postgres) ListApplicationDomains(ctx context.Context, applicationID string) ([]ApplicationDomain, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+domainReturning+`
		FROM application_domains
		WHERE application_id = $1::uuid
		ORDER BY hostname ASC
	`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("list application domains: %w", err)
	}
	defer rows.Close()
	out := make([]ApplicationDomain, 0)
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("scan application domain: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (p *Postgres) GetApplicationDomain(ctx context.Context, applicationID, domainID string) (ApplicationDomain, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return ApplicationDomain{}, err
	}
	domainID, err = ParseUUID(domainID)
	if err != nil {
		return ApplicationDomain{}, err
	}
	out, err := scanDomain(p.db.QueryRowContext(ctx, `
		SELECT `+domainReturning+`
		FROM application_domains
		WHERE id = $1::uuid AND application_id = $2::uuid
	`, domainID, applicationID))
	if errors.Is(err, sql.ErrNoRows) {
		return ApplicationDomain{}, ErrNotFound
	}
	if err != nil {
		return ApplicationDomain{}, fmt.Errorf("get application domain: %w", err)
	}
	return out, nil
}

func (p *Postgres) ActivateApplicationDomain(ctx context.Context, applicationID, domainID string) (ApplicationDomain, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return ApplicationDomain{}, err
	}
	domainID, err = ParseUUID(domainID)
	if err != nil {
		return ApplicationDomain{}, err
	}
	out, err := scanDomain(p.db.QueryRowContext(ctx, `
		UPDATE application_domains
		SET status = $3, updated_at = now()
		WHERE id = $1::uuid AND application_id = $2::uuid
		RETURNING `+domainReturning+`
	`, domainID, applicationID, DomainActive))
	if errors.Is(err, sql.ErrNoRows) {
		return ApplicationDomain{}, ErrNotFound
	}
	if err != nil {
		return ApplicationDomain{}, fmt.Errorf("activate application domain: %w", err)
	}
	return out, nil
}

func (p *Postgres) DeleteApplicationDomain(ctx context.Context, applicationID, domainID string) error {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return err
	}
	domainID, err = ParseUUID(domainID)
	if err != nil {
		return err
	}
	res, err := p.db.ExecContext(ctx, `
		DELETE FROM application_domains
		WHERE id = $1::uuid AND application_id = $2::uuid
	`, domainID, applicationID)
	if err != nil {
		return fmt.Errorf("delete application domain: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListActiveDomains is the Caddy input: hostnames that may receive TLS.
func (p *Postgres) ListActiveDomains(ctx context.Context) ([]ApplicationDomain, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+domainReturning+`
		FROM application_domains
		WHERE status = $1
		ORDER BY hostname ASC
	`, DomainActive)
	if err != nil {
		return nil, fmt.Errorf("list active domains: %w", err)
	}
	defer rows.Close()
	out := make([]ApplicationDomain, 0)
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("scan active domain: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// PlatformHostname is {slug}.{appsDomain} after both pieces are validated.
//
// The slug uses the same rules as local_host, including reserved
// control-plane labels, so an application cannot claim www.forge.com or
// forge.forge.com. The zone is a real DNS name, never an IP or a wildcard.
func PlatformHostname(slug, appsDomain string) (string, error) {
	label, err := ValidateLocalHost(slug)
	if err != nil {
		return "", err
	}
	if label == "" {
		return "", fmt.Errorf("platform hostname requires a slug")
	}
	zone, err := ValidateHostname(appsDomain)
	if err != nil {
		return "", fmt.Errorf("apps domain: %w", err)
	}
	return ValidateHostname(label + "." + zone)
}

// PlatformURL is the HTTPS address Forge will serve for a slug.
// An invalid pair returns "" so JSON can omit it.
func PlatformURL(slug, appsDomain string) string {
	host, err := PlatformHostname(slug, appsDomain)
	if err != nil {
		return ""
	}
	return "https://" + host + "/"
}

// HostnameUnderZone reports whether host is the zone itself or a name
// under it. Custom-domain creates use this so an operator cannot squat
// a platform hostname that Forge assigns from slugs.
func HostnameUnderZone(host, zone string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	zone = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(zone)), ".")
	if host == "" || zone == "" {
		return false
	}
	return host == zone || strings.HasSuffix(host, "."+zone)
}

// EnsurePlatformHost reserves the production hostname for one application.
//
// Empty slug or empty zone releases this app's platform rows. A hostname
// owned by a different application is ErrConflict: the unique index is
// the theft lock, and this read makes the error specific. Calling again
// with the same slug is an update, not a second row. A changed slug
// deletes the previous platform row so the old name can be claimed later.
func (p *Postgres) EnsurePlatformHost(ctx context.Context, applicationID, slug, appsDomain string) (ApplicationDomain, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return ApplicationDomain{}, err
	}
	if strings.TrimSpace(slug) == "" || strings.TrimSpace(appsDomain) == "" {
		if _, err := p.db.ExecContext(ctx, `
			DELETE FROM application_domains
			WHERE application_id = $1::uuid AND origin = $2
		`, applicationID, DomainOriginPlatform); err != nil {
			return ApplicationDomain{}, fmt.Errorf("release platform hostname: %w", err)
		}
		return ApplicationDomain{}, nil
	}
	host, err := PlatformHostname(slug, appsDomain)
	if err != nil {
		return ApplicationDomain{}, err
	}
	var owner string
	err = p.db.QueryRowContext(ctx, `
		SELECT application_id::text FROM application_domains WHERE hostname = $1
	`, host).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ApplicationDomain{}, fmt.Errorf("lookup platform hostname: %w", err)
	}
	if err == nil && owner != applicationID {
		return ApplicationDomain{}, fmt.Errorf("%w: domain is already claimed", ErrConflict)
	}
	if _, err := p.db.ExecContext(ctx, `
		DELETE FROM application_domains
		WHERE application_id = $1::uuid AND origin = $2 AND hostname <> $3
	`, applicationID, DomainOriginPlatform, host); err != nil {
		return ApplicationDomain{}, fmt.Errorf("replace platform hostname: %w", err)
	}
	if owner == applicationID {
		out, err := scanDomain(p.db.QueryRowContext(ctx, `
			UPDATE application_domains
			SET origin = $3, status = $4, environment = $5, updated_at = now()
			WHERE application_id = $1::uuid AND hostname = $2
			RETURNING `+domainReturning+`
		`, applicationID, host, DomainOriginPlatform, DomainActive, EnvProduction))
		if err != nil {
			return ApplicationDomain{}, fmt.Errorf("refresh platform hostname: %w", err)
		}
		return out, nil
	}
	token, err := newDomainToken()
	if err != nil {
		return ApplicationDomain{}, err
	}
	out, err := scanDomain(p.db.QueryRowContext(ctx, `
		INSERT INTO application_domains (application_id, hostname, environment, status, token, origin)
		VALUES ($1::uuid, $2, $3, $4, $5, $6)
		RETURNING `+domainReturning+`
	`, applicationID, host, EnvProduction, DomainActive, token, DomainOriginPlatform))
	if err != nil {
		if isUniqueViolation(err) {
			return ApplicationDomain{}, fmt.Errorf("%w: domain is already claimed", ErrConflict)
		}
		return ApplicationDomain{}, fmt.Errorf("reserve platform hostname: %w", err)
	}
	return out, nil
}
