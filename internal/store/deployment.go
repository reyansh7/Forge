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
// Caddy can emit a Host route without a second query. Environment is
// preview or production (default production). NodeID is which
// worker was asked to run this attempt. NodeAdvertiseHost is joined
// from nodes so Caddy can reach a published port on that machine.
type Deployment struct {
	ID                string
	ProjectID         string
	ApplicationID     string
	Status            Status
	FailedStage       string
	ErrorMessage      string
	RuntimeKind       string
	RuntimeType       string
	PortSource        string
	HostPort          int
	ListenPort        int
	ContainerID       string
	ContainerName     string
	PublicURL         string
	ImageName         string
	BuildLog          string
	RollbackOf        string
	LocalHost         string
	Environment       string
	NodeID            string
	NodeAdvertiseHost string
	CommitSHA         string
	CommitMessage     string
	CommitAuthor      string
	// ActiveJobID is the only queue job allowed to run this row.
	// Re-queue replaces it so a Redis entry from the previous push
	// cannot start a second pipeline. Empty means "not claimed yet"
	// (rows from before migration 0014); the worker still accepts those.
	ActiveJobID string
	// LeaseUntil is when this worker's ownership expires. The zero
	// time with HasLease false means nobody has acquired the attempt.
	// A sweeper treats a past lease as an abandoned pipeline.
	LeaseUntil time.Time
	HasLease   bool
	// RecoveryCount is how many times the sweeper has re-queued this
	// same row. It is not a user-facing retry button.
	RecoveryCount int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const (
	// LeaseTTL is how long a worker owns an attempt after each renew.
	// Longer than LeaseRenewEvery so one missed tick is not expiry.
	LeaseTTL = 90 * time.Second
	// LeaseRenewEvery is the worker tick that extends LeaseUntil.
	LeaseRenewEvery = 30 * time.Second
	// RecoverGrace is how long a queued row may sit without a lease
	// before the sweeper assumes Redis lost the message.
	RecoverGrace = 45 * time.Second
	// MaxRecoveries is the number of automatic re-queues. The next
	// expiry becomes failed so a poison attempt cannot loop.
	MaxRecoveries = 2
)

// ErrLeaseLost means this process no longer owns the attempt.
// The caller must not write status, start another container, or cut over.
var ErrLeaseLost = errors.New("deployment lease lost")

// DurationMS is wall time from create to last status write.
//
// In-progress rows use now so the dashboard can show "how long this
// attempt has been running" without a separate clock column. Terminal
// rows use updated_at. This is Phase 4 observability, not billing.
func (d Deployment) DurationMS() int64 {
	if d.CreatedAt.IsZero() {
		return 0
	}
	end := d.UpdatedAt
	if d.Status.InProgress() || end.Before(d.CreatedAt) {
		end = time.Now().UTC()
	}
	ms := end.Sub(d.CreatedAt).Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}

const maxErrorMessageLen = 4000

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

	return p.CreateDeploymentIn(ctx, applicationID, EnvProduction)
}

// CreateDeploymentIn queues a row in preview or production.
func (p *Postgres) CreateDeploymentIn(ctx context.Context, applicationID, environment string) (Deployment, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return Deployment{}, err
	}
	environment, err = ParseEnvironment(environment)
	if err != nil {
		return Deployment{}, err
	}

	return scanInsertedDeployment(p.db.QueryRowContext(ctx, `
		INSERT INTO deployments (project_id, application_id, status, environment)
		SELECT project_id, id, $2, $3
		FROM applications
		WHERE id = $1::uuid
		RETURNING `+deploymentReturning, applicationID, StatusQueued, environment))
}

// SetDeploymentCommit records the git identity for an auto-deploy.
//
// Called after CreateDeploymentIn, before the TypeDeploy RPUSH, so the
// worker can fetch that SHA and the dashboard can show author/message
// without another GitHub call. Empty values are ignored so a later
// status write cannot wipe a SHA.
func (p *Postgres) SetDeploymentCommit(ctx context.Context, id string, meta CommitMeta) (Deployment, error) {
	id, err := ParseUUID(id)
	if err != nil {
		return Deployment{}, err
	}
	meta = SanitizeCommitMeta(meta)
	if meta.SHA == "" && meta.Message == "" && meta.Author == "" {
		return p.GetDeployment(ctx, id)
	}
	_, err = p.db.ExecContext(ctx, `
		UPDATE deployments
		SET commit_sha = COALESCE(NULLIF($2, ''), commit_sha),
		    commit_message = COALESCE(NULLIF($3, ''), commit_message),
		    commit_author = COALESCE(NULLIF($4, ''), commit_author),
		    updated_at = now()
		WHERE id = $1::uuid
	`, id, meta.SHA, meta.Message, meta.Author)
	if err != nil {
		return Deployment{}, fmt.Errorf("set deployment commit: %w", err)
	}
	return p.GetDeployment(ctx, id)
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
		INSERT INTO deployments (project_id, application_id, status, rollback_of, runtime_kind, image_name, environment, commit_sha, commit_message, commit_author)
		SELECT project_id, application_id, $2, id, runtime_kind, image_name, environment, commit_sha, commit_message, commit_author
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

// ListInterruptedDeployments is every pipeline row past queued on one
// node. Queued still has a Redis job. Detecting/building/… means the
// previous worker already dequeued and died. A second node must not
// fail the first node's in-flight work. Empty nodeID is rejected.
func (p *Postgres) ListInterruptedDeployments(ctx context.Context, nodeID string) ([]Deployment, error) {
	nodeID, err := ParseUUID(nodeID)
	if err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, deploymentSelect+`
		WHERE d.status IN ('detecting','building','provisioning','deploying','health_check')
		  AND d.node_id = $1::uuid
		ORDER BY d.created_at ASC
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list in-progress deployments: %w", err)
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
// Operator Stop uses this path. It does not require a lease, because
// the operator is allowed to close an attempt the worker no longer owns.
func (p *Postgres) UpdateDeployment(ctx context.Context, d Deployment) error {
	return execUpdateDeployment(ctx, p.db, d, "")
}

// UpdateOwnedDeployment is the worker write. It matches active_job_id,
// node_id, and an unexpired lease. A stale pipeline that lost the lease
// gets ErrLeaseLost and must not change the row.
func (p *Postgres) UpdateOwnedDeployment(ctx context.Context, d Deployment) error {
	if strings.TrimSpace(d.ActiveJobID) == "" || strings.TrimSpace(d.NodeID) == "" {
		return fmt.Errorf("owned update requires job id and node id")
	}
	return execUpdateDeployment(ctx, p.db, d, d.ActiveJobID)
}

// CutoverLive is the rolling-deploy database step.
//
// Caddy has already switched the Host to next. This transaction marks
// the previous same-environment LIVE row STOPPED first, then writes
// next as LIVE. The unique index on (application_id, environment)
// WHERE live still holds: both writes are one transaction, old LIVE
// is gone before new LIVE is inserted into the index.
//
// Failures here must not stop the previous container; the worker
// restores Caddy from remaining LIVE rows and fails only next.
func (p *Postgres) CutoverLive(ctx context.Context, next Deployment) ([]Deployment, error) {
	id, err := ParseUUID(next.ID)
	if err != nil {
		return nil, err
	}
	applicationID, err := ParseUUID(next.ApplicationID)
	if err != nil {
		return nil, err
	}
	env, err := ParseEnvironment(next.Environment)
	if err != nil {
		return nil, err
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("cutover live: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, deploymentSelect+`
		WHERE d.application_id = $1::uuid
		  AND d.status = $2
		FOR UPDATE OF d
	`, applicationID, StatusLive)
	if err != nil {
		return nil, fmt.Errorf("cutover live: lock: %w", err)
	}
	locked, err := scanDeployments(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}

	prev := make([]Deployment, 0)
	for _, d := range locked {
		if d.ID == id || !SameEnvironment(d.Environment, env) {
			continue
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE deployments
			SET status = $2,
			    failed_stage = NULL,
			    error_message = NULLIF($3, ''),
			    updated_at = now()
			WHERE id = $1::uuid
		`, d.ID, StatusStopped, SanitizeErrorMessage("replaced by a newer deployment"))
		if err != nil {
			return nil, fmt.Errorf("cutover live: stop previous: %w", err)
		}
		d.Status = StatusStopped
		d.FailedStage = ""
		d.ErrorMessage = SanitizeErrorMessage("replaced by a newer deployment")
		prev = append(prev, d)
	}

	next.Status = StatusLive
	next.FailedStage = ""
	next.ErrorMessage = ""
	// A job id means this cutover must still own the lease. If the
	// sweeper already expired it, the transaction rolls back and the
	// previous LIVE row stays LIVE.
	own := ""
	if strings.TrimSpace(next.ActiveJobID) != "" {
		own = next.ActiveJobID
	}
	if err := execUpdateDeployment(ctx, tx, next, own); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("cutover live: commit: %w", err)
	}
	return prev, nil
}

type dbExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func execUpdateDeployment(ctx context.Context, db dbExec, d Deployment, ownedJobID string) error {
	id, err := ParseUUID(d.ID)
	if err != nil {
		return err
	}
	d.ErrorMessage = SanitizeErrorMessage(d.ErrorMessage)
	d.BuildLog = SanitizeBuildLog(d.BuildLog)
	ownedJobID = strings.TrimSpace(ownedJobID)
	nodeID := strings.TrimSpace(d.NodeID)
	if ownedJobID != "" {
		if nodeID, err = ParseUUID(nodeID); err != nil {
			return err
		}
	}
	// image_name / build_log: empty on a status-only write must not wipe
	// a previously stored artifact. Rollback copies image_name at INSERT;
	// the worker fills build_log during docker build.
	//
	// ownedJobID empty is the operator path (Stop, sweeper, tests).
	// A non-empty id additionally requires that job and an unexpired lease,
	// so a pipeline that lost ownership cannot mark LIVE.
	res, err := db.ExecContext(ctx, `
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
			container_name = COALESCE(NULLIF($11, ''), container_name),
			listen_port = COALESCE(NULLIF($12, 0), listen_port),
			runtime_type = COALESCE(NULLIF($13, ''), runtime_type),
			port_source = COALESCE(NULLIF($14, ''), port_source),
			node_id = COALESCE(NULLIF($15, '')::uuid, node_id),
			updated_at = now()
		WHERE id = $1::uuid
		  AND ($16 = '' OR (
		        active_job_id = $16
		        AND node_id = NULLIF($17, '')::uuid
		        AND lease_until IS NOT NULL
		        AND lease_until > now()
		      ))
	`, id, d.Status, d.FailedStage, d.ErrorMessage, d.RuntimeKind, d.HostPort, d.ContainerID, d.PublicURL, d.ImageName, d.BuildLog, d.ContainerName, d.ListenPort, d.RuntimeType, d.PortSource, d.NodeID, ownedJobID, nodeID)
	if err != nil {
		return fmt.Errorf("update deployment: %w", err)
	}
	if ownedJobID != "" {
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrLeaseLost
		}
	}
	return nil
}

const deploymentReturning = `
	id::text, project_id::text, application_id::text, status, COALESCE(failed_stage, ''),
	COALESCE(error_message, ''), COALESCE(runtime_kind, ''),
	COALESCE(host_port, 0), COALESCE(container_id, ''),
	COALESCE(public_url, ''), COALESCE(image_name, ''), COALESCE(build_log, ''),
	COALESCE(rollback_of::text, ''), created_at, updated_at,
	COALESCE(container_name, ''), COALESCE(listen_port, 0),
	COALESCE(runtime_type, ''), COALESCE(port_source, ''),
	COALESCE(node_id::text, ''), COALESCE(environment, 'production'),
	COALESCE(commit_sha, ''), COALESCE(commit_message, ''), COALESCE(commit_author, ''),
	COALESCE(active_job_id, ''),
	COALESCE(lease_until, TIMESTAMP 'epoch'),
	(lease_until IS NOT NULL),
	COALESCE(recovery_count, 0)
`

const deploymentSelect = `
	SELECT d.id::text, d.project_id::text, d.application_id::text, d.status,
	       COALESCE(d.failed_stage, ''), COALESCE(d.error_message, ''),
	       COALESCE(d.runtime_kind, ''), COALESCE(d.host_port, 0),
	       COALESCE(d.container_id, ''), COALESCE(d.public_url, ''),
	       COALESCE(d.image_name, ''), COALESCE(d.build_log, ''),
	       COALESCE(d.rollback_of::text, ''), d.created_at, d.updated_at,
	       COALESCE(d.container_name, ''), COALESCE(d.listen_port, 0),
	       COALESCE(d.runtime_type, ''), COALESCE(d.port_source, ''),
	       COALESCE(d.node_id::text, ''),
	       COALESCE(d.environment, 'production'),
	       COALESCE(d.commit_sha, ''), COALESCE(d.commit_message, ''), COALESCE(d.commit_author, ''),
	       COALESCE(d.active_job_id, ''),
	       COALESCE(d.lease_until, TIMESTAMP 'epoch'),
	       (d.lease_until IS NOT NULL),
	       COALESCE(d.recovery_count, 0),
	       COALESCE(a.local_host, ''),
	       COALESCE(n.advertise_host, '')
	FROM deployments d
	JOIN applications a ON a.id = d.application_id
	LEFT JOIN nodes n ON n.id = d.node_id
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
		&d.ContainerName, &d.ListenPort, &d.RuntimeType, &d.PortSource,
		&d.NodeID, &d.Environment,
		&d.CommitSHA, &d.CommitMessage, &d.CommitAuthor, &d.ActiveJobID,
		&d.LeaseUntil, &d.HasLease, &d.RecoveryCount,
	}
}

// ClaimQueuedJob records the job id allowed to execute this row.
//
// The UPDATE matches only status=queued. A re-queue therefore cannot
// restart a row the worker has already moved to detecting, and a second
// RPUSH of an older job id is ignored by the worker. Postgres stays the
// source of truth; Redis is still a transient list.
func (p *Postgres) ClaimQueuedJob(ctx context.Context, id, jobID string) (Deployment, error) {
	id, err := ParseUUID(id)
	if err != nil {
		return Deployment{}, err
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return Deployment{}, fmt.Errorf("job id is required")
	}
	row := p.db.QueryRowContext(ctx, `
		UPDATE deployments
		SET active_job_id = $2, updated_at = now()
		WHERE id = $1::uuid AND status = 'queued'
		RETURNING `+deploymentReturning, id, jobID)
	d, err := scanInsertedDeployment(row)
	if err == nil {
		return d, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Deployment{}, err
	}
	if _, getErr := p.GetDeployment(ctx, id); getErr != nil {
		return Deployment{}, getErr
	}
	return Deployment{}, fmt.Errorf("%w: only a queued deployment can be re-queued", ErrConflict)
}

// AcquireLease lets one worker own a queued attempt.
//
// The UPDATE matches status=queued, the current active_job_id, and a
// missing or already-expired lease. Two workers popping the same job
// cannot both win. The loser gets ErrLeaseLost and must not build.
func (p *Postgres) AcquireLease(ctx context.Context, id, jobID, nodeID string, ttl time.Duration) (Deployment, error) {
	id, err := ParseUUID(id)
	if err != nil {
		return Deployment{}, err
	}
	nodeID, err = ParseUUID(nodeID)
	if err != nil {
		return Deployment{}, err
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return Deployment{}, fmt.Errorf("job id is required")
	}
	if ttl <= 0 {
		ttl = LeaseTTL
	}
	row := p.db.QueryRowContext(ctx, `
		UPDATE deployments
		SET lease_until = now() + $4::interval,
		    node_id = $3::uuid,
		    updated_at = now()
		WHERE id = $1::uuid
		  AND status = 'queued'
		  AND active_job_id = $2
		  AND (lease_until IS NULL OR lease_until < now())
		RETURNING `+deploymentReturning, id, jobID, nodeID, secondsInterval(ttl))
	d, err := scanInsertedDeployment(row)
	if err == nil {
		return d, nil
	}
	if errors.Is(err, ErrNotFound) {
		return Deployment{}, ErrLeaseLost
	}
	return Deployment{}, err
}

// RenewLease extends ownership. Zero rows means the sweeper or another
// worker took the attempt. The caller must cancel the pipeline.
func (p *Postgres) RenewLease(ctx context.Context, id, jobID, nodeID string, ttl time.Duration) error {
	id, err := ParseUUID(id)
	if err != nil {
		return err
	}
	nodeID, err = ParseUUID(nodeID)
	if err != nil {
		return err
	}
	if ttl <= 0 {
		ttl = LeaseTTL
	}
	res, err := p.db.ExecContext(ctx, `
		UPDATE deployments
		SET lease_until = now() + $4::interval, updated_at = now()
		WHERE id = $1::uuid
		  AND active_job_id = $2
		  AND node_id = $3::uuid
		  AND lease_until > now()
	`, id, strings.TrimSpace(jobID), nodeID, secondsInterval(ttl))
	if err != nil {
		return fmt.Errorf("renew lease: %w", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}

// ExpireNodeLeases marks this node's in-flight attempts abandoned.
//
// Called when that worker process starts. The previous process is gone,
// so its lease must not keep the sweeper waiting. Queued rows are left
// alone: they have not been acquired, and a Redis message may still
// be valid. Does not change LIVE rows.
func (p *Postgres) ExpireNodeLeases(ctx context.Context, nodeID string) (int, error) {
	nodeID, err := ParseUUID(nodeID)
	if err != nil {
		return 0, err
	}
	res, err := p.db.ExecContext(ctx, `
		UPDATE deployments
		SET lease_until = now() - interval '1 second', updated_at = now()
		WHERE node_id = $1::uuid
		  AND status IN ('detecting','building','provisioning','deploying','health_check')
	`, nodeID)
	if err != nil {
		return 0, fmt.Errorf("expire node leases: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func secondsInterval(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("%d seconds", secs)
}

// ListRecoverableDeployments is the sweeper input.
//
// Queued with no lease, older than grace: Redis likely dropped the
// message. In-progress with an expired lease: the worker stopped
// renewing. LIVE rows are never returned.
func (p *Postgres) ListRecoverableDeployments(ctx context.Context, grace time.Duration) ([]Deployment, error) {
	if grace <= 0 {
		grace = RecoverGrace
	}
	rows, err := p.db.QueryContext(ctx, deploymentSelect+`
		WHERE (
		  d.status = 'queued'
		  AND d.lease_until IS NULL
		  AND d.updated_at < now() - $1::interval
		) OR (
		  d.status IN ('detecting','building','provisioning','deploying','health_check')
		  AND d.lease_until IS NOT NULL
		  AND d.lease_until < now()
		)
		ORDER BY d.updated_at ASC
	`, secondsInterval(grace))
	if err != nil {
		return nil, fmt.Errorf("list recoverable deployments: %w", err)
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// RequeueRecoverable puts an abandoned early-stage attempt back on queued
// with a new job id. False means another sweeper or a renewed lease won.
// provisioning and later are refused: a container may already exist, and
// a second run would duplicate it.
func (p *Postgres) RequeueRecoverable(ctx context.Context, id, expectedStatus, jobID string, max int, grace time.Duration) (Deployment, bool, error) {
	id, err := ParseUUID(id)
	if err != nil {
		return Deployment{}, false, err
	}
	expectedStatus = strings.TrimSpace(expectedStatus)
	switch Status(expectedStatus) {
	case StatusQueued, StatusDetecting, StatusBuilding:
	default:
		return Deployment{}, false, nil
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return Deployment{}, false, fmt.Errorf("job id is required")
	}
	if max < 1 {
		max = MaxRecoveries
	}
	if grace <= 0 {
		grace = RecoverGrace
	}
	row := p.db.QueryRowContext(ctx, `
		UPDATE deployments
		SET status = 'queued',
		    active_job_id = $2,
		    recovery_count = recovery_count + 1,
		    lease_until = NULL,
		    failed_stage = NULL,
		    error_message = '',
		    updated_at = now()
		WHERE id = $1::uuid
		  AND status = $3
		  AND recovery_count < $4
		  AND (
		    (status = 'queued' AND lease_until IS NULL AND updated_at < now() - $5::interval)
		    OR (status IN ('detecting','building') AND lease_until IS NOT NULL AND lease_until < now())
		  )
		RETURNING `+deploymentReturning, id, jobID, expectedStatus, max, secondsInterval(grace))
	d, err := scanInsertedDeployment(row)
	if errors.Is(err, ErrNotFound) {
		return Deployment{}, false, nil
	}
	if err != nil {
		return Deployment{}, false, err
	}
	return d, true, nil
}

// FailRecoverable closes an attempt the sweeper will not run again.
// The WHERE clause cannot match a LIVE row. Previous releases stay up
// because this statement names one deployment id.
func (p *Postgres) FailRecoverable(ctx context.Context, id, expectedStatus, message string, max int, grace time.Duration) (bool, error) {
	id, err := ParseUUID(id)
	if err != nil {
		return false, err
	}
	if max < 1 {
		max = MaxRecoveries
	}
	if grace <= 0 {
		grace = RecoverGrace
	}
	message = SanitizeErrorMessage(message)
	res, err := p.db.ExecContext(ctx, `
		UPDATE deployments
		SET status = 'failed',
		    failed_stage = status,
		    error_message = NULLIF($2, ''),
		    lease_until = NULL,
		    updated_at = now()
		WHERE id = $1::uuid
		  AND status = $3
		  AND status <> 'live'
		  AND (
		    (
		      status = 'queued'
		      AND lease_until IS NULL
		      AND updated_at < now() - $4::interval
		      AND recovery_count >= $5
		    )
		    OR (
		      status IN ('detecting','building','provisioning','deploying','health_check')
		      AND lease_until IS NOT NULL
		      AND lease_until < now()
		    )
		  )
	`, id, message, strings.TrimSpace(expectedStatus), secondsInterval(grace), max)
	if err != nil {
		return false, fmt.Errorf("fail recoverable deployment: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func deploymentSelectDest(d *Deployment) []any {
	return append(deploymentInsertDest(d), &d.LocalHost, &d.NodeAdvertiseHost)
}

// CountInProgressByOwner is the Phase 5 deploy-concurrency quota.
func (p *Postgres) CountInProgressByOwner(ctx context.Context, ownerID string) (int, error) {
	ownerID, err := ParseUUID(ownerID)
	if err != nil {
		return 0, err
	}
	var n int
	err = p.db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM deployments d
		JOIN applications a ON a.id = d.application_id
		JOIN projects p ON p.id = a.project_id
		WHERE p.owner_id = $1::uuid
		  AND d.status IN ('queued','detecting','building','provisioning','deploying','health_check')
	`, ownerID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count in-progress deploys: %w", err)
	}
	return n, nil
}

// PruneOldBuildLogs clears build_log on terminal rows older than age.
// Retention is a quota, not a legal archive. Values are already sanitized.
func (p *Postgres) PruneOldBuildLogs(ctx context.Context, age time.Duration) (int64, error) {
	if age <= 0 {
		return 0, nil
	}
	res, err := p.db.ExecContext(ctx, `
		UPDATE deployments
		SET build_log = ''
		WHERE build_log <> ''
		  AND status IN ('live','failed','stopped')
		  AND updated_at < now() - $1::interval
	`, fmt.Sprintf("%d seconds", int(age.Seconds())))
	if err != nil {
		return 0, fmt.Errorf("prune build logs: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
