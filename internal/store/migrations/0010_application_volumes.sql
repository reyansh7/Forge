-- Phase 7 increment 7.2: operator-declared persistent volumes.
-- Postgres stores the intent (name + container path). Docker named
-- volumes hold the bytes. Host bind mounts are not a column and must
-- never be accepted — they would expose .forge/, the Docker socket,
-- or another app's data. ON DELETE CASCADE drops the row when the
-- application is deleted; the worker/API still best-effort
-- `docker volume rm` so disk does not leak on this node.
CREATE TABLE IF NOT EXISTS application_volumes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    name TEXT NOT NULL
        CHECK (char_length(name) BETWEEN 1 AND 32)
        CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$'),
    mount_path TEXT NOT NULL
        CHECK (char_length(mount_path) BETWEEN 2 AND 128)
        CHECK (mount_path LIKE '/%'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS application_volumes_app_name
    ON application_volumes (application_id, name);

CREATE UNIQUE INDEX IF NOT EXISTS application_volumes_app_path
    ON application_volumes (application_id, mount_path);
