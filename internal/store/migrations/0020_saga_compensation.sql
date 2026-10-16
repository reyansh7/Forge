-- Compensation state for a project deployment saga.
--
-- previous_deployment_id is the healthy release that was serving
-- before this attempt was promoted. The worker keeps that container
-- running until the saga commits. promoted_at orders compensating
-- rollbacks (latest promotion is undone first).
--
-- compensation is empty until a rollback is attempted. "restored"
-- means routing points at the previous release again. "failed" means
-- the compensating action did not finish; compensation_error says why.
-- An empty previous release cannot be restored by starting a new
-- image, because that can replay migrations.
--
-- committed_at is set only when every required service is live. That
-- is when the held previous containers may be stopped.

ALTER TABLE project_deployment_services
    ADD COLUMN IF NOT EXISTS previous_deployment_id UUID REFERENCES deployments (id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS promoted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS compensation TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS compensation_error TEXT NOT NULL DEFAULT '';

ALTER TABLE project_deployments
    ADD COLUMN IF NOT EXISTS committed_at TIMESTAMPTZ;
