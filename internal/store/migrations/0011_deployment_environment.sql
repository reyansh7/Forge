-- Phase 7 increment 7.3: preview vs production on each deployment.
-- Existing rows default to production so current Host routing is unchanged.
-- LIVE uniqueness is per (application, environment): production and
-- preview may both be LIVE. Host isolation is a Caddy slug suffix, not
-- a second application row. This is not custom domains.
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS environment TEXT NOT NULL DEFAULT 'production';

ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_environment_check;

ALTER TABLE deployments
    ADD CONSTRAINT deployments_environment_check
    CHECK (environment IN ('preview', 'production'));

DROP INDEX IF EXISTS deployments_one_live_per_application;

CREATE UNIQUE INDEX IF NOT EXISTS deployments_one_live_per_app_env
    ON deployments (application_id, environment)
    WHERE status = 'live';
