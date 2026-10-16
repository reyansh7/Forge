-- Phase 7 increment 7.1: explicit same-project service links.
-- A source app receives the target's Host URL as env at deploy time.
-- This is address injection through Caddy, not a container overlay.
CREATE TABLE IF NOT EXISTS application_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_application_id UUID NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    target_application_id UUID NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    env_key TEXT NOT NULL
        CHECK (char_length(env_key) BETWEEN 1 AND 64)
        CHECK (env_key ~ '^[A-Za-z_][A-Za-z0-9_]*$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (source_application_id <> target_application_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS application_links_source_key
    ON application_links (source_application_id, env_key);
