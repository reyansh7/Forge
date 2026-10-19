-- Increment 7.14: index of images whose build inputs were hashed.
--
-- The row is an index, not the artifact. The artifact is the local
-- Docker image named in image_name. Deleting a row forgets the index.
-- It does not delete the image, a volume, or a deployment.
--
-- cache_key is a sha256 hex digest computed by the worker from the
-- build directory and public build env. Clients cannot supply it.
-- image_id is the docker inspect ID recorded after a successful build.
-- Reuse is allowed only when inspect still returns that ID.
--
-- A runtime secret is not stored here. The key is a digest.

CREATE TABLE IF NOT EXISTS build_cache (
    application_id UUID NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    cache_key TEXT NOT NULL
        CHECK (cache_key ~ '^[0-9a-f]{64}$'),
    image_name TEXT NOT NULL
        CHECK (image_name ~ '^forge-app-[0-9a-f]{32}$'),
    image_id TEXT NOT NULL
        CHECK (image_id ~ '^sha256:[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (application_id, cache_key)
);

CREATE INDEX IF NOT EXISTS build_cache_app_created_idx
    ON build_cache (application_id, created_at DESC);
