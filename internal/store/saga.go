package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SagaSibling is one service in the rollout that contains a deployment.
//
// Status is the child deployment status when a deployment exists, and
// the enqueue status otherwise. PromotedAt is set only after that
// service's traffic switch. Compensation is empty until a rollback runs.
type SagaSibling struct {
	ServiceID            string
	Name                 string
	DeploymentID         string
	PreviousDeploymentID string
	Status               string
	PromotedAt           time.Time
	Compensation         string
	LocalHost            string
}

// SagaView is the durable saga around one deployment.
//
// InSaga is false for an ordinary single-application deploy. Those
// deploys keep the existing cutover: the previous container is stopped
// as soon as the new one is healthy.
type SagaView struct {
	InSaga      bool
	ProjectID   string
	RolloutID   string
	Status      string
	Committed   bool
	ServiceName string
	LocalHost   string
	Siblings    []SagaSibling
}

// ProjectNetworkName is the Docker bridge for one project.
//
// The name is derived from the project UUID. It is not taken from the
// repository, so a service cannot choose another project's network or
// the host network.
func ProjectNetworkName(projectID string) (string, error) {
	id, err := ParseUUID(projectID)
	if err != nil {
		return "", err
	}
	hex := strings.ReplaceAll(id, "-", "")
	if len(hex) < 12 {
		return "", fmt.Errorf("project id is too short for a network name")
	}
	return "forgep-" + hex[:12], nil
}

// InternalServiceURL is the address of a container on its project network.
//
// alias is the DNS label Docker publishes on that bridge. The port is
// the container listen port, not the host publish port. Callers on
// another network, including the public internet, cannot use it.
func InternalServiceURL(alias string, port int) string {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return ""
	}
	if port < 1 || port > 65535 {
		port = 8080
	}
	return fmt.Sprintf("http://%s:%d/", alias, port)
}

// SagaView loads the rollout that owns deploymentID.
//
// A deployment that was queued on its own is not an error and not a saga.
func (p *Postgres) SagaView(ctx context.Context, deploymentID string) (SagaView, error) {
	deploymentID, err := ParseUUID(deploymentID)
	if err != nil {
		return SagaView{}, err
	}
	var view SagaView
	var committed sql.NullTime
	err = p.db.QueryRowContext(ctx, `
		SELECT pd.id::text, pd.project_id::text, pd.status, pd.committed_at,
		       me.service_name, COALESCE(a.local_host, '')
		FROM project_deployment_services me
		JOIN project_deployments pd ON pd.id = me.project_deployment_id
		JOIN applications a ON a.id = me.application_id
		WHERE me.deployment_id = $1::uuid
	`, deploymentID).Scan(&view.RolloutID, &view.ProjectID, &view.Status, &committed, &view.ServiceName, &view.LocalHost)
	if errors.Is(err, sql.ErrNoRows) {
		return SagaView{}, nil
	}
	if err != nil {
		return SagaView{}, fmt.Errorf("saga view: %w", err)
	}
	view.InSaga = true
	view.Committed = committed.Valid
	rows, err := p.db.QueryContext(ctx, `
		SELECT s.id::text, s.service_name, s.deployment_id::text, s.previous_deployment_id::text,
		       COALESCE(d.status, s.status), s.promoted_at, s.compensation, COALESCE(a.local_host, '')
		FROM project_deployment_services s
		JOIN applications a ON a.id = s.application_id
		LEFT JOIN deployments d ON d.id = s.deployment_id
		WHERE s.project_deployment_id = $1::uuid
		ORDER BY s.promoted_at ASC NULLS LAST, s.created_at ASC
	`, view.RolloutID)
	if err != nil {
		return SagaView{}, fmt.Errorf("saga siblings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sib SagaSibling
		var dep, prev sql.NullString
		var promoted sql.NullTime
		if err := rows.Scan(&sib.ServiceID, &sib.Name, &dep, &prev, &sib.Status, &promoted, &sib.Compensation, &sib.LocalHost); err != nil {
			return SagaView{}, err
		}
		sib.DeploymentID = dep.String
		sib.PreviousDeploymentID = prev.String
		if promoted.Valid {
			sib.PromotedAt = promoted.Time
		}
		view.Siblings = append(view.Siblings, sib)
	}
	if err := rows.Err(); err != nil {
		return SagaView{}, err
	}
	return view, nil
}

// MarkPromoted records that this deployment's traffic switch succeeded
// and which release was healthy before it. previousID may be empty on
// the first release of a service.
func (p *Postgres) MarkPromoted(ctx context.Context, deploymentID, previousID string) error {
	deploymentID, err := ParseUUID(deploymentID)
	if err != nil {
		return err
	}
	var prev any
	if strings.TrimSpace(previousID) != "" {
		id, err := ParseUUID(previousID)
		if err != nil {
			return err
		}
		prev = id
	}
	res, err := p.db.ExecContext(ctx, `
		UPDATE project_deployment_services
		SET previous_deployment_id = COALESCE($2::uuid, previous_deployment_id),
		    promoted_at = COALESCE(promoted_at, now())
		WHERE deployment_id = $1::uuid
	`, deploymentID, prev)
	if err != nil {
		return fmt.Errorf("mark promoted: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CommitIfReady stops being "held" only when every required service is
// live and the saga was not already failed or committed.
//
// The returned deployments are the previous releases whose containers
// the worker may now stop. A second call returns committed=false so a
// retry does not stop them twice.
func (p *Postgres) CommitIfReady(ctx context.Context, deploymentID string) ([]Deployment, bool, error) {
	view, err := p.SagaView(ctx, deploymentID)
	if err != nil || !view.InSaga || view.Committed || view.Status == "failed" {
		return nil, false, err
	}
	if len(view.Siblings) == 0 {
		return nil, false, nil
	}
	var prevIDs []string
	for _, sib := range view.Siblings {
		if sib.Status != string(StatusLive) {
			return nil, false, nil
		}
		if sib.PreviousDeploymentID != "" {
			prevIDs = append(prevIDs, sib.PreviousDeploymentID)
		}
	}
	res, err := p.db.ExecContext(ctx, `
		UPDATE project_deployments
		SET status = 'live', committed_at = now(), updated_at = now()
		WHERE id = $1::uuid AND status = 'deploying' AND committed_at IS NULL
	`, view.RolloutID)
	if err != nil {
		return nil, false, fmt.Errorf("commit saga: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if n == 0 {
		return nil, false, nil
	}
	var prev []Deployment
	for _, id := range prevIDs {
		d, err := p.GetDeployment(ctx, id)
		if err != nil {
			return prev, true, err
		}
		prev = append(prev, d)
	}
	return prev, true, nil
}

// RestorePrevious makes the earlier release LIVE again and the promoted
// release stopped. The unique live index requires the new row to leave
// LIVE before the previous row enters it. The worker still has to point
// Caddy at the result and must not assume that switch succeeded.
func (p *Postgres) RestorePrevious(ctx context.Context, newID, previousID string) error {
	newID, err := ParseUUID(newID)
	if err != nil {
		return err
	}
	previousID, err = ParseUUID(previousID)
	if err != nil {
		return err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		UPDATE deployments
		SET status = $2, error_message = $3, updated_at = now()
		WHERE id = $1::uuid AND status = $4
	`, newID, StatusStopped, SanitizeErrorMessage("saga compensation restored the previous release"), StatusLive); err != nil {
		return fmt.Errorf("compensation stop promoted: %w", err)
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE deployments
		SET status = $2, error_message = '', updated_at = now()
		WHERE id = $1::uuid AND status = $3
	`, previousID, StatusLive, StatusStopped)
	if err != nil {
		return fmt.Errorf("compensation restore previous: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("previous release is not a stopped deployment that can be restored")
	}
	return tx.Commit()
}

// RecordCompensation stores the outcome of one compensating action.
// state is "restored" or "failed". A failed compensation is not retried
// into success by this method.
func (p *Postgres) RecordCompensation(ctx context.Context, serviceID, state, message string) error {
	serviceID, err := ParseUUID(serviceID)
	if err != nil {
		return err
	}
	if state != "restored" && state != "failed" {
		return fmt.Errorf("invalid compensation state")
	}
	_, err = p.db.ExecContext(ctx, `
		UPDATE project_deployment_services
		SET compensation = $2, compensation_error = $3
		WHERE id = $1::uuid
	`, serviceID, state, SanitizeErrorMessage(message))
	if err != nil {
		return fmt.Errorf("record compensation: %w", err)
	}
	return nil
}

// SetBuildPackage persists a control-plane Go package choice.
//
// It does not write the repository. The worker still has to rebuild
// and health-check before the release can be promoted.
func (p *Postgres) SetBuildPackage(ctx context.Context, applicationID, pkg string) error {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return err
	}
	pkg, err = ValidateBuildPackage(pkg)
	if err != nil {
		return err
	}
	res, err := p.db.ExecContext(ctx, `
		UPDATE applications SET build_package = $2, updated_at = now()
		WHERE id = $1::uuid
	`, applicationID, pkg)
	if err != nil {
		return fmt.Errorf("set build package: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
