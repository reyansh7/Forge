package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Status is one step (or terminal) of the Phase 0 deployment machine.
//
// Architecture requires an explicit stage so a failure is not a generic
// "it broke". The worker writes these; HTTP only creates queued rows.
type Status string

const (
	StatusQueued       Status = "queued"
	StatusDetecting    Status = "detecting"
	StatusBuilding     Status = "building"
	StatusProvisioning Status = "provisioning"
	StatusDeploying    Status = "deploying"
	StatusHealthCheck  Status = "health_check"
	StatusLive         Status = "live"
	StatusFailed       Status = "failed"
	StatusStopped      Status = "stopped"
)

// InProgress is a non-terminal pipeline step. A second Deploy while
// one of these is active would start another container beside the first.
func (s Status) InProgress() bool {
	switch s {
	case StatusQueued, StatusDetecting, StatusBuilding, StatusProvisioning, StatusDeploying, StatusHealthCheck:
		return true
	default:
		return false
	}
}

// Deployment is one attempt to take an application through the loop.
//
// Security: fields other than IDs originate from the control plane
// (worker). Clients cannot set container_id, public_url, or image_name
// on create. Rollback copies image_name from a prior row the API loaded.
//
// ImageName / BuildLog / RollbackOf are Phase 2: history and rollback
// need an artifact that survives after the container is removed.
// LocalHost is denormalized from applications at read time (JOIN) so
// Caddy can emit a Host route without a second query.
type Deployment struct {
	ID            string
	ProjectID     string
	ApplicationID string
	Status        Status
	FailedStage   string
	ErrorMessage  string
	RuntimeKind   string
	HostPort      int
	ContainerID   string
	PublicURL     string
	ImageName     string
	BuildLog      string
	RollbackOf    string
	LocalHost     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const maxErrorMessageLen = 500

// SanitizeErrorMessage trims and caps worker errors for storage.
// Do not put connection strings or clone URLs with userinfo here.
func SanitizeErrorMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) > maxErrorMessageLen {
		return msg[:maxErrorMessageLen]
	}
	return msg
}

// CreateDeployment inserts a queued row. The HTTP handler then enqueues
// a deploy job. Status starts queued so a crash before RPUSH is visible.
func (p *Postgres) CreateDeployment(ctx context.Context, applicationID string) (Deployment, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return Deployment{}, err
	}

	return scanInsertedDeployment(p.db.QueryRowContext(ctx, `
		INSERT INTO deployments (project_id, application_id, status)
		SELECT project_id, id, $2
		FROM applications
		WHERE id = $1::uuid
		RETURNING `+deploymentReturning, applicationID, StatusQueued))
}

// CreateRollbackDeployment queues a new attempt that reuses a prior image.
//
// The source must already have an image_name the worker wrote on a
// successful build. Clients cannot pass an image or command — we copy
// those columns from the source row. Missing/empty image is ErrNotFound
// so HTTP can map it to 409 without leaking SQL.
func (p *Postgres) CreateRollbackDeployment(ctx context.Context, sourceID string) (Deployment, error) {
	sourceID, err := ParseUUID(sourceID)
	if err != nil {
		return Deployment{}, err
	}
	d, err := scanInsertedDeployment(p.db.QueryRowContext(ctx, `
		INSERT INTO deployments (project_id, application_id, status, rollback_of, runtime_kind, image_name)
		SELECT project_id, application_id, $2, id, runtime_kind, image_name
		FROM deployments
		WHERE id = $1::uuid
		  AND image_name <> ''
		RETURNING `+deploymentReturning, sourceID, StatusQueued))
	if errors.Is(err, ErrNotFound) {
		return Deployment{}, fmt.Errorf("%w: source has no image to roll back to", ErrNotFound)
	}
	return d, err
}

// GetDeployment loads one row. sql.ErrNoRows becomes ErrNotFound.
func (p *Postgres) GetDeployment(ctx context.Context, id string) (Deployment, error) {
	id, err := ParseUUID(id)
	if err != nil {
		return Deployment{}, err
	}
	return scanDeployment(p.db.QueryRowContext(ctx, deploymentSelect+` WHERE d.id = $1::uuid`, id))
}

// ListDeploymentsByProject returns newest first. Empty is a non-nil slice.
func (p *Postgres) ListDeploymentsByProject(ctx context.Context, projectID string) ([]Deployment, error) {
	projectID, err := ParseUUID(projectID)
	if err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, deploymentSelect+`
		WHERE d.project_id = $1::uuid
		ORDER BY d.created_at DESC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// ListDeploymentsByApplication returns newest first for one app.
func (p *Postgres) ListDeploymentsByApplication(ctx context.Context, applicationID string) ([]Deployment, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, deploymentSelect+`
		WHERE d.application_id = $1::uuid
		ORDER BY d.created_at DESC
	`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// ListLiveDeployments is the Caddy input: every currently routed app.
func (p *Postgres) ListLiveDeployments(ctx context.Context) ([]Deployment, error) {
	rows, err := p.db.QueryContext(ctx, deploymentSelect+`
		WHERE d.status = $1
		ORDER BY d.created_at ASC
	`, StatusLive)
	if err != nil {
		return nil, fmt.Errorf("list live deployments: %w", err)
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// UpdateDeployment persists worker progress. updated_at is set here so
// the API can show "last transition" without a trigger.
func (p *Postgres) UpdateDeployment(ctx context.Context, d Deployment) error {
	id, err := ParseUUID(d.ID)
	if err != nil {
		return err
	}
	d.ErrorMessage = SanitizeErrorMessage(d.ErrorMessage)
	d.BuildLog = SanitizeBuildLog(d.BuildLog)
	// image_name / build_log: empty on a status-only write must not wipe
	// a previously stored artifact. Rollback copies image_name at INSERT;
	// the worker fills build_log during docker build.
	_, err = p.db.ExecContext(ctx, `
		UPDATE deployments SET
			status = $2,
			failed_stage = NULLIF($3, ''),
			error_message = NULLIF($4, ''),
			runtime_kind = NULLIF($5, ''),
			host_port = NULLIF($6, 0),
			container_id = NULLIF($7, ''),
			public_url = NULLIF($8, ''),
			image_name = COALESCE(NULLIF($9, ''), image_name),
			build_log = CASE WHEN $10 <> '' THEN $10 ELSE build_log END,
			updated_at = now()
		WHERE id = $1::uuid
	`, id, d.Status, d.FailedStage, d.ErrorMessage, d.RuntimeKind, d.HostPort, d.ContainerID, d.PublicURL, d.ImageName, d.BuildLog)
	if err != nil {
		return fmt.Errorf("update deployment: %w", err)
	}
	return nil
}

const deploymentReturning = `
	id::text, project_id::text, application_id::text, status, COALESCE(failed_stage, ''),
	COALESCE(error_message, ''), COALESCE(runtime_kind, ''),
	COALESCE(host_port, 0), COALESCE(container_id, ''),
	COALESCE(public_url, ''), COALESCE(image_name, ''), COALESCE(build_log, ''),
	COALESCE(rollback_of::text, ''), created_at, updated_at
`

const deploymentSelect = `
	SELECT d.id::text, d.project_id::text, d.application_id::text, d.status,
	       COALESCE(d.failed_stage, ''), COALESCE(d.error_message, ''),
	       COALESCE(d.runtime_kind, ''), COALESCE(d.host_port, 0),
	       COALESCE(d.container_id, ''), COALESCE(d.public_url, ''),
	       COALESCE(d.image_name, ''), COALESCE(d.build_log, ''),
	       COALESCE(d.rollback_of::text, ''), d.created_at, d.updated_at,
	       COALESCE(a.local_host, '')
	FROM deployments d
	JOIN applications a ON a.id = d.application_id
`

func scanInsertedDeployment(row *sql.Row) (Deployment, error) {
	var out Deployment
	err := row.Scan(deploymentInsertDest(&out)...)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("insert deployment: %w", err)
	}
	return out, nil
}

func scanDeployment(row *sql.Row) (Deployment, error) {
	var out Deployment
	err := row.Scan(deploymentSelectDest(&out)...)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("get deployment: %w", err)
	}
	return out, nil
}

func scanDeployments(rows *sql.Rows) ([]Deployment, error) {
	out := make([]Deployment, 0)
	for rows.Next() {
		var item Deployment
		if err := rows.Scan(deploymentSelectDest(&item)...); err != nil {
			return nil, fmt.Errorf("scan deployment: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	return out, nil
}

func deploymentInsertDest(d *Deployment) []any {
	return []any{
		&d.ID, &d.ProjectID, &d.ApplicationID, &d.Status, &d.FailedStage, &d.ErrorMessage,
		&d.RuntimeKind, &d.HostPort, &d.ContainerID, &d.PublicURL, &d.ImageName, &d.BuildLog,
		&d.RollbackOf, &d.CreatedAt, &d.UpdatedAt,
	}
}

func deploymentSelectDest(d *Deployment) []any {
	return append(deploymentInsertDest(d), &d.LocalHost)
}
