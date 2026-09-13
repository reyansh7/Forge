-- Phase 6: worker/runtime nodes. Placement is a store + scheduler
-- concern, not an HTTP handler. token_hash is SHA-256 hex of the
-- join token (same shape as sessions). The raw token is shown once.
CREATE TABLE IF NOT EXISTS nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE
        CHECK (char_length(name) BETWEEN 1 AND 63)
        CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
    token_hash TEXT NOT NULL
        CHECK (char_length(token_hash) = 64),
    status TEXT NOT NULL DEFAULT 'ready'
        CHECK (status IN ('ready', 'draining', 'dead')),
    advertise_host TEXT NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS nodes_token_hash_idx ON nodes (token_hash);

-- Which node was asked to run this deployment. NULL is a pre-Phase-6 row.
ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS node_id UUID REFERENCES nodes (id);
