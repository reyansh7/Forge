-- Increment 0.4: durable Deployment records for the Phase 0 local loop.
--
-- PostgreSQL is the source of truth for where a deploy is in the
-- DETECT → BUILD → PROVISION → DEPLOY → HEALTH CHECK → LIVE|FAILED
-- machine. Redis only transports the "please run this" signal.
--
-- No applications table: Phase 0 deploys a project directly. A separate
-- application entity is Phase 1.
--
-- failed_stage is set only on FAILED so the API can say *where* it
-- stopped, not only that it failed.
--
-- container_id / host_port / public_url are runtime metadata written by
-- the worker after isolation is in place. They are not user input.
--
-- ON DELETE CASCADE: removing a project removes its deployments. Phase 0
-- has no project DELETE API yet; this keeps the schema consistent.

CREATE TABLE IF NOT EXISTS deployments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    status TEXT NOT NULL
        CHECK (status IN (
            'queued',
            'detecting',
            'building',
            'provisioning',
            'deploying',
            'health_check',
            'live',
            'failed'
        )),
    failed_stage TEXT,
    error_message TEXT,
    runtime_kind TEXT,
    host_port INTEGER
        CHECK (host_port IS NULL OR (host_port >= 1 AND host_port <= 65535)),
    container_id TEXT,
    public_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS deployments_project_id_idx ON deployments (project_id);
CREATE INDEX IF NOT EXISTS deployments_status_idx ON deployments (status);
