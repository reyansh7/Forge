-- Increment 0.2: durable Project records for the Forge control plane.
--
-- What this is: a SQL migration. Migrate() runs it once and records the
-- filename in schema_migrations. Restarting the API is then a no-op here.
--
-- Who uses it: the Go API (internal/store). User applications do not
-- connect to this database in Phase 0.
--
-- repository_url is TEXT metadata only. This file does not clone git,
-- fetch the URL, or execute anything from the repository (untrusted code).
--
-- id: UUID assigned by Postgres (gen_random_uuid, built into PG 13+).
--   The API does not pick IDs, so two processes cannot collide on insert.
-- name / repository_url: CHECK constraints match Go validation. The
--   database is the last line of defense if a caller skips Validate.
-- TIMESTAMPTZ: store instants in UTC-aware form; JSON conversion uses UTC.
--
-- INDEX on created_at DESC: GET /projects ORDER BY created_at DESC.
-- IF NOT EXISTS: safe if someone re-runs the SQL by hand.
--
-- Deferred: unique(name), applications, deployments, users, FKs.

CREATE TABLE IF NOT EXISTS projects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL
        CHECK (char_length(name) BETWEEN 1 AND 100),
    repository_url TEXT NOT NULL
        CHECK (char_length(repository_url) BETWEEN 1 AND 2048),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS projects_created_at_idx ON projects (created_at DESC);
