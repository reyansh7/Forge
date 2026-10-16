package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ProjectDeployment is one Deploy All saga for a project.
//
// Status deploying means child jobs are still running or were queued.
// Status live means every required child deployment is live. Status
// failed means at least one required service did not become live, or
// the operator cancelled. Failed is not partial success.
type ProjectDeployment struct {
	ID           string
	ProjectID    string
	Status       string
	ErrorMessage string
	Note         string
	Plan         []byte
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ProjectDeploymentService is one service inside that saga.
//
// Status here is only the enqueue result (queued or failed to queue).
// The worker's deployment row is the runtime status. DeploymentID is
// empty when the job was never created.
type ProjectDeploymentService struct {
	ID                   string
	ProjectDeploymentID  string
	ApplicationID        string
	DeploymentID         string
	ServiceName          string
	RootDirectory        string
	BuildPackage         string
	Status               string
	ErrorMessage         string
	PreviousDeploymentID string
	PromotedAt           time.Time
	Compensation         string
	CompensationError    string
	CreatedAt            time.Time
}

func (p *Postgres) CreateProjectDeployment(ctx context.Context, projectID, note string, plan []byte) (ProjectDeployment, error) {
	projectID, err := ParseUUID(projectID)
	if err != nil {
		return ProjectDeployment{}, err
	}
	if len(plan) == 0 {
		plan = []byte(`{}`)
	}
	var out ProjectDeployment
	err = p.db.QueryRowContext(ctx, `
		INSERT INTO project_deployments (project_id, status, note, plan)
		VALUES ($1::uuid, 'deploying', $2, $3::jsonb)
		RETURNING id::text, project_id::text, status, error_message, note, plan::text, created_at, updated_at
	`, projectID, note, string(plan)).Scan(
		&out.ID, &out.ProjectID, &out.Status, &out.ErrorMessage, &out.Note, &out.Plan, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return ProjectDeployment{}, fmt.Errorf("insert project deployment: %w", err)
	}
	return out, nil
}

func (p *Postgres) AddProjectDeploymentService(ctx context.Context, in ProjectDeploymentService) (ProjectDeploymentService, error) {
	rolloutID, err := ParseUUID(in.ProjectDeploymentID)
	if err != nil {
		return ProjectDeploymentService{}, err
	}
	appID, err := ParseUUID(in.ApplicationID)
	if err != nil {
		return ProjectDeploymentService{}, err
	}
	var dep any
	if in.DeploymentID != "" {
		id, err := ParseUUID(in.DeploymentID)
		if err != nil {
			return ProjectDeploymentService{}, err
		}
		dep = id
	}
	status := in.Status
	if status != "queued" && status != "failed" {
		return ProjectDeploymentService{}, fmt.Errorf("invalid project service status")
	}
	var out ProjectDeploymentService
	var depOut sql.NullString
	err = p.db.QueryRowContext(ctx, `
		INSERT INTO project_deployment_services (
			project_deployment_id, application_id, deployment_id, service_name,
			root_directory, build_package, status, error_message
		)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8)
		RETURNING id::text, project_deployment_id::text, application_id::text, deployment_id::text,
		          service_name, root_directory, build_package, status, error_message, created_at
	`, rolloutID, appID, dep, in.ServiceName, in.RootDirectory, in.BuildPackage, status, in.ErrorMessage).Scan(
		&out.ID, &out.ProjectDeploymentID, &out.ApplicationID, &depOut,
		&out.ServiceName, &out.RootDirectory, &out.BuildPackage, &out.Status, &out.ErrorMessage, &out.CreatedAt,
	)
	if err != nil {
		return ProjectDeploymentService{}, fmt.Errorf("insert project deployment service: %w", err)
	}
	out.DeploymentID = depOut.String
	return out, nil
}

// SetProjectDeploymentStatus moves a saga that is not already live.
// A live project deployment is not reopened by a late poll.
func (p *Postgres) SetProjectDeploymentStatus(ctx context.Context, id, status, message string) error {
	id, err := ParseUUID(id)
	if err != nil {
		return err
	}
	if status != "deploying" && status != "failed" && status != "live" {
		return fmt.Errorf("invalid project deployment status")
	}
	res, err := p.db.ExecContext(ctx, `
		UPDATE project_deployments
		SET status = $2,
		    error_message = CASE WHEN $3 = '' THEN error_message ELSE $3 END,
		    updated_at = now()
		WHERE id = $1::uuid AND status <> 'live'
	`, id, status, message)
	if err != nil {
		return fmt.Errorf("update project deployment: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var cur string
		err := p.db.QueryRowContext(ctx, `SELECT status FROM project_deployments WHERE id = $1::uuid`, id).Scan(&cur)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if cur == "live" && status != "live" {
			return nil
		}
	}
	return nil
}

func (p *Postgres) GetProjectDeployment(ctx context.Context, id string) (ProjectDeployment, []ProjectDeploymentService, error) {
	id, err := ParseUUID(id)
	if err != nil {
		return ProjectDeployment{}, nil, err
	}
	var out ProjectDeployment
	err = p.db.QueryRowContext(ctx, `
		SELECT id::text, project_id::text, status, error_message, note, plan::text, created_at, updated_at
		FROM project_deployments WHERE id = $1::uuid
	`, id).Scan(&out.ID, &out.ProjectID, &out.Status, &out.ErrorMessage, &out.Note, &out.Plan, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectDeployment{}, nil, ErrNotFound
	}
	if err != nil {
		return ProjectDeployment{}, nil, fmt.Errorf("get project deployment: %w", err)
	}
	svcs, err := p.listProjectDeploymentServices(ctx, id)
	if err != nil {
		return ProjectDeployment{}, nil, err
	}
	return out, svcs, nil
}

func (p *Postgres) ListProjectDeployments(ctx context.Context, projectID string) ([]ProjectDeployment, error) {
	projectID, err := ParseUUID(projectID)
	if err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT id::text, project_id::text, status, error_message, note, plan::text, created_at, updated_at
		FROM project_deployments
		WHERE project_id = $1::uuid
		ORDER BY created_at DESC
		LIMIT 20
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project deployments: %w", err)
	}
	defer rows.Close()
	var out []ProjectDeployment
	for rows.Next() {
		var item ProjectDeployment
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Status, &item.ErrorMessage, &item.Note, &item.Plan, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (p *Postgres) listProjectDeploymentServices(ctx context.Context, rolloutID string) ([]ProjectDeploymentService, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id::text, project_deployment_id::text, application_id::text, deployment_id::text,
		       service_name, root_directory, build_package, status, error_message,
		       previous_deployment_id::text, promoted_at, compensation, compensation_error, created_at
		FROM project_deployment_services
		WHERE project_deployment_id = $1::uuid
		ORDER BY created_at ASC
	`, rolloutID)
	if err != nil {
		return nil, fmt.Errorf("list project deployment services: %w", err)
	}
	defer rows.Close()
	var out []ProjectDeploymentService
	for rows.Next() {
		var item ProjectDeploymentService
		var dep, prev sql.NullString
		var promoted sql.NullTime
		if err := rows.Scan(&item.ID, &item.ProjectDeploymentID, &item.ApplicationID, &dep, &item.ServiceName, &item.RootDirectory, &item.BuildPackage, &item.Status, &item.ErrorMessage, &prev, &promoted, &item.Compensation, &item.CompensationError, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.DeploymentID = dep.String
		item.PreviousDeploymentID = prev.String
		if promoted.Valid {
			item.PromotedAt = promoted.Time
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// MarkProjectDeployFailed stops later services in the same saga from
// replacing a healthy release. A deployment that is not part of a
// project rollout updates zero rows.
func (p *Postgres) MarkProjectDeployFailed(ctx context.Context, deploymentID, message string) error {
	deploymentID, err := ParseUUID(deploymentID)
	if err != nil {
		return err
	}
	message = SanitizeErrorMessage(message)
	_, err = p.db.ExecContext(ctx, `
		UPDATE project_deployments pd
		SET status = 'failed',
		    error_message = CASE WHEN pd.error_message = '' THEN $2 ELSE pd.error_message END,
		    updated_at = now()
		FROM project_deployment_services s
		WHERE s.project_deployment_id = pd.id
		  AND s.deployment_id = $1::uuid
		  AND pd.status <> 'live'
	`, deploymentID, message)
	if err != nil {
		return fmt.Errorf("mark project deployment failed: %w", err)
	}
	return nil
}

// ProjectDeployBlockReason is empty when this deployment may be
// promoted. A failed saga, including operator cancel, returns the
// reason so the worker can stop before Caddy switches traffic.
func (p *Postgres) ProjectDeployBlockReason(ctx context.Context, deploymentID string) (string, error) {
	deploymentID, err := ParseUUID(deploymentID)
	if err != nil {
		return "", err
	}
	var status, msg string
	err = p.db.QueryRowContext(ctx, `
		SELECT pd.status, pd.error_message
		FROM project_deployment_services s
		JOIN project_deployments pd ON pd.id = s.project_deployment_id
		WHERE s.deployment_id = $1::uuid
	`, deploymentID).Scan(&status, &msg)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("project deploy gate: %w", err)
	}
	if status != "failed" {
		return "", nil
	}
	if msg == "" {
		msg = "a required service failed"
	}
	return msg, nil
}
