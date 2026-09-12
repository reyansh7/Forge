-- Runtime metadata so the dashboard can show the real container,
-- listen port, and runtime type after detect (not only docker build).
--
-- container_name is the docker --name written by the worker
-- (<app-slug>-<short-id>). Empty on rows created before this
-- migration; those still resolve to forge-run-<uuid>.
--
-- listen_port is the container-side port (not host_port).
-- runtime_type is server|static|image. port_source explains why
-- listen_port was chosen (forge_json, source_listen, pack, …).

ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS container_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS listen_port INTEGER
        CHECK (listen_port IS NULL OR (listen_port >= 1 AND listen_port <= 65535)),
    ADD COLUMN IF NOT EXISTS runtime_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS port_source TEXT NOT NULL DEFAULT '';
