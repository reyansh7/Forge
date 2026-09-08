-- Phase 1: applications sit between projects and deployments.
--
-- A project is a folder. An application is the deployable unit: git
-- remote, env vars, and a live container. Phase 0 deployed a project
-- directly; this migration backfills one application named "app" per
-- project so existing rows keep working.
--
-- application_env_vars hold operator-supplied KEY=VALUE metadata.
-- Values are not secrets-management (Phase 6). They must not be logged.
-- The worker injects them as docker -e after validation. PORT and FORGE_*
-- are reserved for the control plane.
--
-- deployments.application_id is required after backfill. Replacing a
-- live deploy is per application, not per project (two apps in one
-- project may both be LIVE).
--
-- status 'stopped' is operator-initiated basic management (Phase 1).

CREATE TABLE IF NOT EXISTS applications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name TEXT NOT NULL
        CHECK (char_length(name) BETWEEN 1 AND 100),
    repository_url TEXT NOT NULL
        CHECK (char_length(repository_url) BETWEEN 1 AND 2048),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);

CREATE INDEX IF NOT EXISTS applications_project_id_idx ON applications (project_id);

CREATE TABLE IF NOT EXISTS application_env_vars (
    application_id UUID NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    key TEXT NOT NULL
        CHECK (char_length(key) BETWEEN 1 AND 64),
    value TEXT NOT NULL
        CHECK (char_length(value) <= 4096),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (application_id, key)
);

INSERT INTO applications (project_id, name, repository_url, created_at, updated_at)
SELECT p.id, 'app', p.repository_url, p.created_at, p.updated_at
FROM projects p
WHERE NOT EXISTS (
    SELECT 1 FROM applications a WHERE a.project_id = p.id AND a.name = 'app'
);

ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS application_id UUID REFERENCES applications (id) ON DELETE CASCADE;

UPDATE deployments d
SET application_id = a.id
FROM applications a
WHERE d.application_id IS NULL
  AND a.project_id = d.project_id
  AND a.name = 'app';

ALTER TABLE deployments
    ALTER COLUMN application_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS deployments_application_id_idx ON deployments (application_id);

ALTER TABLE deployments DROP CONSTRAINT IF EXISTS deployments_status_check;

ALTER TABLE deployments ADD CONSTRAINT deployments_status_check CHECK (status IN (
    'queued',
    'detecting',
    'building',
    'provisioning',
    'deploying',
    'health_check',
    'live',
    'failed',
    'stopped'
));
