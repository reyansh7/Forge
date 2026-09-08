package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
)

// Application is the deployable unit inside a project.
//
// A project groups apps. An application owns the git remote, env vars,
// settings, and deployments. Two apps in one project may both be LIVE.
//
// RootDirectory / HealthPath / LocalHost are Phase 2 operator settings.
// They change how the next deploy builds, probes, and is routed — they
// do not mutate a container that is already running.
type Application struct {
	ID            string
	ProjectID     string
	Name          string
	RepositoryURL string
	RootDirectory string
	HealthPath    string
	LocalHost     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ApplicationInput is a validated create/update payload.
type ApplicationInput struct {
	Name          string
	RepositoryURL string
	RootDirectory string
	HealthPath    string
	LocalHost     string
}

// ValidateApplicationInput matches project URL rules plus a name.
func ValidateApplicationInput(name, repositoryURL string) (ApplicationInput, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ApplicationInput{}, fmt.Errorf("name is required")
	}
	if utf8.RuneCountInString(name) > maxProjectNameLen {
		return ApplicationInput{}, fmt.Errorf("name must be at most %d characters", maxProjectNameLen)
	}
	if containsCtl(name) {
		return ApplicationInput{}, fmt.Errorf("name contains invalid characters")
	}
	// Dummy project name: we only want the repository_url checks.
	urlIn, err := ValidateProjectInput("app", repositoryURL)
	if err != nil {
		return ApplicationInput{}, err
	}
	return ApplicationInput{Name: name, RepositoryURL: urlIn.RepositoryURL}, nil
}

func (p *Postgres) CreateApplication(ctx context.Context, projectID string, in ApplicationInput) (Application, error) {
	projectID, err := ParseUUID(projectID)
	if err != nil {
		return Application{}, err
	}
	in, err = ValidateApplication(in.Name, in.RepositoryURL, in.RootDirectory, in.HealthPath, in.LocalHost)
	if err != nil {
		return Application{}, err
	}

	var out Application
	err = p.db.QueryRowContext(ctx, `
		INSERT INTO applications (project_id, name, repository_url, root_directory, health_path, local_host)
		VALUES ($1::uuid, $2, $3, $4, $5, $6)
		RETURNING id::text, project_id::text, name, repository_url, root_directory, health_path, local_host, created_at, updated_at
	`, projectID, in.Name, in.RepositoryURL, in.RootDirectory, in.HealthPath, in.LocalHost).Scan(
		&out.ID, &out.ProjectID, &out.Name, &out.RepositoryURL, &out.RootDirectory, &out.HealthPath, &out.LocalHost, &out.CreatedAt, &out.UpdatedAt,
	)
	if isUniqueViolation(err) {
		return Application{}, conflictApplication(err)
	}
	if err != nil {
		return Application{}, fmt.Errorf("insert application: %w", err)
	}
	return out, nil
}

func (p *Postgres) GetApplication(ctx context.Context, id string) (Application, error) {
	id, err := ParseUUID(id)
	if err != nil {
		return Application{}, err
	}
	return scanApplication(p.db.QueryRowContext(ctx, applicationSelect+` WHERE id = $1::uuid`, id))
}

func (p *Postgres) ListApplicationsByProject(ctx context.Context, projectID string) ([]Application, error) {
	projectID, err := ParseUUID(projectID)
	if err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, applicationSelect+`
		WHERE project_id = $1::uuid
		ORDER BY created_at ASC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	defer rows.Close()
	return scanApplications(rows)
}

func (p *Postgres) UpdateApplication(ctx context.Context, id string, in ApplicationInput) (Application, error) {
	id, err := ParseUUID(id)
	if err != nil {
		return Application{}, err
	}
	in, err = ValidateApplication(in.Name, in.RepositoryURL, in.RootDirectory, in.HealthPath, in.LocalHost)
	if err != nil {
		return Application{}, err
	}
	var out Application
	err = p.db.QueryRowContext(ctx, `
		UPDATE applications
		SET name = $2, repository_url = $3, root_directory = $4, health_path = $5, local_host = $6, updated_at = now()
		WHERE id = $1::uuid
		RETURNING id::text, project_id::text, name, repository_url, root_directory, health_path, local_host, created_at, updated_at
	`, id, in.Name, in.RepositoryURL, in.RootDirectory, in.HealthPath, in.LocalHost).Scan(
		&out.ID, &out.ProjectID, &out.Name, &out.RepositoryURL, &out.RootDirectory, &out.HealthPath, &out.LocalHost, &out.CreatedAt, &out.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Application{}, ErrNotFound
	}
	if isUniqueViolation(err) {
		return Application{}, conflictApplication(err)
	}
	if err != nil {
		return Application{}, fmt.Errorf("update application: %w", err)
	}
	return out, nil
}

// DeleteApplication removes the app, its env rows, and its deployments
// (ON DELETE CASCADE). The HTTP layer must stop a live container first
// so a deleted row cannot leave an orphan process.
func (p *Postgres) DeleteApplication(ctx context.Context, id string) error {
	id, err := ParseUUID(id)
	if err != nil {
		return err
	}
	res, err := p.db.ExecContext(ctx, `DELETE FROM applications WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("delete application: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

const applicationSelect = `
	SELECT id::text, project_id::text, name, repository_url,
	       COALESCE(root_directory, '.'), COALESCE(health_path, '/'), COALESCE(local_host, ''),
	       created_at, updated_at
	FROM applications
`

func scanApplication(row *sql.Row) (Application, error) {
	var out Application
	err := row.Scan(&out.ID, &out.ProjectID, &out.Name, &out.RepositoryURL, &out.RootDirectory, &out.HealthPath, &out.LocalHost, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Application{}, ErrNotFound
	}
	if err != nil {
		return Application{}, fmt.Errorf("get application: %w", err)
	}
	return out, nil
}

func scanApplications(rows *sql.Rows) ([]Application, error) {
	out := make([]Application, 0)
	for rows.Next() {
		var item Application
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Name, &item.RepositoryURL, &item.RootDirectory, &item.HealthPath, &item.LocalHost, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan application: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	return out, nil
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func conflictApplication(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && strings.Contains(pg.ConstraintName, "local_host") {
		return fmt.Errorf("%w: local_host is already taken", ErrConflict)
	}
	return fmt.Errorf("%w: application name already exists in this project", ErrConflict)
}
