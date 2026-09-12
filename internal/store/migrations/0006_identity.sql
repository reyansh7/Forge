-- Phase 3: control-plane identity, ownership, and audit.
--
-- Until this migration, loopback was the only gate: anyone who could
-- reach 127.0.0.1:8080 could create projects and read env values.
-- That is not authorization. A UUID in a URL is not proof of access.
--
-- users: operators of Forge (not end-users of deployed apps).
--   password_hash is a bcrypt hash. The plaintext never lands in SQL.
--   There is no "admin" role column yet — team RBAC is a later phase.
--   Username uniqueness is case-insensitive (lower(username)).
--
-- sessions: opaque bearer tokens. We store SHA-256(token), never the
--   token itself, so a database dump cannot be replayed as login.
--
-- projects.owner_id: the operator who created the project (or who
--   claimed it on first bootstrap). NULL is only for rows created
--   before this migration; bootstrap assigns those to the first user.
--   List/Get/mutate must not treat a missing owner as "public".
--
-- audit_events: security-sensitive control-plane actions.
--   metadata must not contain env values, passwords, or session tokens.
--   actor_id is nullable so a failed login (unknown user) can still
--   be recorded without inventing a user row.
--
-- This file does not bind the API to 0.0.0.0. Loopback remains the
-- network publication until a later increment names a different bind.

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username TEXT NOT NULL
        CHECK (char_length(username) BETWEEN 3 AND 32),
    password_hash TEXT NOT NULL
        CHECK (char_length(password_hash) BETWEEN 20 AND 255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_idx
    ON users (lower(username));

CREATE TABLE IF NOT EXISTS sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL
        CHECK (char_length(token_hash) = 64),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS sessions_token_hash_idx
    ON sessions (token_hash);

CREATE INDEX IF NOT EXISTS sessions_user_id_idx
    ON sessions (user_id);

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS owner_id UUID REFERENCES users (id);

CREATE INDEX IF NOT EXISTS projects_owner_id_idx
    ON projects (owner_id);

CREATE TABLE IF NOT EXISTS audit_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id UUID REFERENCES users (id) ON DELETE SET NULL,
    action TEXT NOT NULL
        CHECK (char_length(action) BETWEEN 1 AND 64),
    resource_type TEXT NOT NULL
        CHECK (char_length(resource_type) BETWEEN 1 AND 64),
    resource_id TEXT
        CHECK (resource_id IS NULL OR char_length(resource_id) BETWEEN 1 AND 64),
    ip TEXT
        CHECK (ip IS NULL OR char_length(ip) BETWEEN 1 AND 64),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS audit_events_created_at_idx
    ON audit_events (created_at DESC);

CREATE INDEX IF NOT EXISTS audit_events_actor_id_idx
    ON audit_events (actor_id);
