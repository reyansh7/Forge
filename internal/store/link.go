package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ApplicationLink is an explicit edge: source deploy receives target's
// Host URL in env_key.
//
// What it is: service discovery without a mesh. Why: two apps in one
// project (frontend → API) should not paste a URL that drifts. How:
// Caddy already routes http://{slug}.localhost:9080/. The worker
// injects that address. Containers still cannot reach control-plane
// Postgres/Redis. Cross-project targets are rejected so one owner's
// other project cannot be bound by id-guessing.
//
// Deferred: overlay networks, private DNS inside Docker.
type ApplicationLink struct {
	ID                  string
	SourceApplicationID string
	TargetApplicationID string
	EnvKey              string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

const maxApplicationLinks = 16

// ServiceHostURL is the address a sibling should call.
//
// Prefer the operator slug; otherwise the same suggested slug the
// worker assigns on deploy. Path URLs (/d/{id}/) change every attempt
// and are the wrong contract for a stable env var.
func ServiceHostURL(proxyBase string, app Application) string {
	slug := strings.TrimSpace(app.LocalHost)
	if slug == "" {
		slug = SuggestedLocalHost(app.Name, app.ID)
	}
	return HostURLForSlug(proxyBase, slug)
}

// HostURLForSlug mirrors proxy.HostPublicURL without importing proxy
// (store must not depend on Caddy). Tests lock the host:port shape.
func HostURLForSlug(proxyBase, slug string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return ""
	}
	port := "9080"
	if u, err := url.Parse(strings.TrimSpace(proxyBase)); err == nil && u.Port() != "" {
		port = u.Port()
	}
	return "http://" + slug + ".localhost:" + port + "/"
}

func (p *Postgres) CreateApplicationLink(ctx context.Context, sourceID, targetID, envKey string) (ApplicationLink, error) {
	sourceID, err := ParseUUID(sourceID)
	if err != nil {
		return ApplicationLink{}, err
	}
	targetID, err = ParseUUID(targetID)
	if err != nil {
		return ApplicationLink{}, err
	}
	if err := ValidateEnvKey(envKey); err != nil {
		return ApplicationLink{}, err
	}
	envKey = strings.TrimSpace(envKey)
	if sourceID == targetID {
		return ApplicationLink{}, fmt.Errorf("cannot link an application to itself")
	}

	src, err := p.GetApplication(ctx, sourceID)
	if err != nil {
		return ApplicationLink{}, err
	}
	tgt, err := p.GetApplication(ctx, targetID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ApplicationLink{}, ErrNotFound
		}
		return ApplicationLink{}, err
	}
	if src.ProjectID != tgt.ProjectID {
		// Same 404 as a missing target: a UUID from another project
		// must not confirm that the application exists.
		return ApplicationLink{}, ErrNotFound
	}

	n, err := p.countLinks(ctx, sourceID)
	if err != nil {
		return ApplicationLink{}, err
	}
	if n >= maxApplicationLinks {
		return ApplicationLink{}, fmt.Errorf("at most %d service links per application", maxApplicationLinks)
	}

	var out ApplicationLink
	err = p.db.QueryRowContext(ctx, `
		INSERT INTO application_links (source_application_id, target_application_id, env_key)
		VALUES ($1::uuid, $2::uuid, $3)
		RETURNING id::text, source_application_id::text, target_application_id::text, env_key, created_at, updated_at
	`, sourceID, targetID, envKey).Scan(&out.ID, &out.SourceApplicationID, &out.TargetApplicationID, &out.EnvKey, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return ApplicationLink{}, fmt.Errorf("%w: env key is already linked", ErrConflict)
		}
		return ApplicationLink{}, fmt.Errorf("create application link: %w", err)
	}
	return out, nil
}

func (p *Postgres) countLinks(ctx context.Context, sourceID string) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx, `
		SELECT count(*) FROM application_links WHERE source_application_id = $1::uuid
	`, sourceID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count application links: %w", err)
	}
	return n, nil
}

func (p *Postgres) ListApplicationLinks(ctx context.Context, sourceID string) ([]ApplicationLink, error) {
	sourceID, err := ParseUUID(sourceID)
	if err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT id::text, source_application_id::text, target_application_id::text, env_key, created_at, updated_at
		FROM application_links
		WHERE source_application_id = $1::uuid
		ORDER BY env_key ASC
	`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("list application links: %w", err)
	}
	defer rows.Close()
	out := make([]ApplicationLink, 0)
	for rows.Next() {
		var l ApplicationLink
		if err := rows.Scan(&l.ID, &l.SourceApplicationID, &l.TargetApplicationID, &l.EnvKey, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan application link: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteApplicationLink(ctx context.Context, sourceID, linkID string) error {
	sourceID, err := ParseUUID(sourceID)
	if err != nil {
		return err
	}
	linkID, err = ParseUUID(linkID)
	if err != nil {
		return err
	}
	res, err := p.db.ExecContext(ctx, `
		DELETE FROM application_links
		WHERE id = $1::uuid AND source_application_id = $2::uuid
	`, linkID, sourceID)
	if err != nil {
		return fmt.Errorf("delete application link: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
