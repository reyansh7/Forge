package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Sentinel errors the HTTP layer maps with errors.Is.
//
// ErrNotFound → 404. ErrInvalidID → 400.
// Driver/SQL errors are wrapped and must not be copied into HTTP bodies
// (they can leak schema or connection details).
var (
	ErrNotFound  = errors.New("project not found")
	ErrInvalidID = errors.New("invalid project id")
)

// uuidPattern is the 8-4-4-4-12 hex form Postgres accepts for uuid.
// Validating in Go means GET /projects/not-a-uuid is 400, not a 500 from
// the driver failing to parse the parameter.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const (
	maxProjectNameLen = 100
	maxRepoURLLen     = 2048
)

// Project is one row of control-plane state (the Go view of `projects`).
//
// Responsibility: the in-memory shape handlers JSON-encode.
// Called by: Postgres.Create/Get/List and httpapi (as store.Project).
//
// Security: RepositoryURL is a string we persist. Storing it does not
// clone, HTTP-GET, or exec the remote. That would be SSRF / untrusted code.
// No auth in Phase 0 — any client on loopback can insert rows.
//
// Deferred: owner user_id, applications, deployments.
type Project struct {
	ID            string
	Name          string
	RepositoryURL string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ProjectInput is a create payload after validation (trimmed, length-checked).
type ProjectInput struct {
	Name          string
	RepositoryURL string
}

// ValidateProjectInput is the API/store contract for "this is insertable".
//
// HTTP calls it so 400 happens before a round-trip. CreateProject calls it
// again so a future worker cannot skip the HTTP layer and insert garbage.
// It never fetches the URL (no SSRF in 0.2).
//
// utf8.RuneCountInString: length is user-visible characters, not bytes
// (a name of 100 emoji should not sneak past a byte cap).
func ValidateProjectInput(name, repositoryURL string) (ProjectInput, error) {
	name = strings.TrimSpace(name)
	repositoryURL = strings.TrimSpace(repositoryURL)

	if name == "" {
		return ProjectInput{}, fmt.Errorf("name is required")
	}
	if utf8.RuneCountInString(name) > maxProjectNameLen {
		return ProjectInput{}, fmt.Errorf("name must be at most %d characters", maxProjectNameLen)
	}
	if containsCtl(name) {
		return ProjectInput{}, fmt.Errorf("name contains invalid characters")
	}

	if repositoryURL == "" {
		return ProjectInput{}, fmt.Errorf("repository_url is required")
	}
	if utf8.RuneCountInString(repositoryURL) > maxRepoURLLen {
		return ProjectInput{}, fmt.Errorf("repository_url must be at most %d characters", maxRepoURLLen)
	}
	if containsCtl(repositoryURL) {
		return ProjectInput{}, fmt.Errorf("repository_url contains invalid characters")
	}
	if !validRepositoryURL(repositoryURL) {
		return ProjectInput{}, fmt.Errorf("repository_url must be an http(s), git, or ssh URL")
	}

	return ProjectInput{Name: name, RepositoryURL: repositoryURL}, nil
}

// containsCtl rejects CR/LF/NUL. A newline in `name` could split log lines
// or HTTP headers if a later increment interpolates the value unsafely.
func containsCtl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// validRepositoryURL accepts common git remotes without dialing them.
//
// Allowed: https://…, http://…, ssh://…, git://…, git@host:path.git
// Rejected: file:// (host filesystem), javascript:, data:, empty host.
// A later clone increment must not find a surprising scheme already stored.
func validRepositoryURL(raw string) bool {
	if strings.HasPrefix(raw, "git@") {
		// SCP-like GitHub/GitLab remote: git@github.com:org/repo.git
		_, path, ok := strings.Cut(raw, ":")
		return ok && path != "" && !strings.Contains(raw, "://")
	}

	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ssh", "git":
		return true
	default:
		return false
	}
}

// ParseProjectID canonicalizes a path id. Unknown shape → ErrInvalidID
// (HTTP 400), not a database error (HTTP 500).
func ParseProjectID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if !uuidPattern.MatchString(id) {
		return "", ErrInvalidID
	}
	return strings.ToLower(id), nil
}

// CreateProject INSERTs a row and returns what Postgres stored
// (generated UUID + timestamps). ctx cancels the query if the client hangs up.
//
// $1 / $2 are bound parameters — the name never becomes SQL text
// (SQL injection). %w wraps the driver error for logs, not for the client.
func (p *Postgres) CreateProject(ctx context.Context, in ProjectInput) (Project, error) {
	in, err := ValidateProjectInput(in.Name, in.RepositoryURL)
	if err != nil {
		return Project{}, err
	}

	var out Project
	err = p.db.QueryRowContext(ctx, `
		INSERT INTO projects (name, repository_url)
		VALUES ($1, $2)
		RETURNING id::text, name, repository_url, created_at, updated_at
	`, in.Name, in.RepositoryURL).Scan(
		&out.ID,
		&out.Name,
		&out.RepositoryURL,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return Project{}, fmt.Errorf("insert project: %w", err)
	}
	return out, nil
}

// GetProject loads one row. sql.ErrNoRows becomes ErrNotFound so httpapi
// does not import database/sql to tell 404 from 500.
func (p *Postgres) GetProject(ctx context.Context, id string) (Project, error) {
	id, err := ParseProjectID(id)
	if err != nil {
		return Project{}, err
	}

	var out Project
	err = p.db.QueryRowContext(ctx, `
		SELECT id::text, name, repository_url, created_at, updated_at
		FROM projects
		WHERE id = $1::uuid
	`, id).Scan(
		&out.ID,
		&out.Name,
		&out.RepositoryURL,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("get project: %w", err)
	}
	return out, nil
}

// ListProjects returns every project, newest first.
//
// make([]Project, 0) is a non-nil empty slice. JSON encodes that as []
// rather than null — nicer for clients. defer rows.Close() returns the
// connection to the pool; skipping Close leaks pool slots under load.
// rows.Err() catches iteration errors that Next() swallowed.
//
// Deferred: pagination. An unbounded list is acceptable for local Phase 0.
func (p *Postgres) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id::text, name, repository_url, created_at, updated_at
		FROM projects
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	out := make([]Project, 0)
	for rows.Next() {
		var item Project
		if err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.RepositoryURL,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	return out, nil
}
