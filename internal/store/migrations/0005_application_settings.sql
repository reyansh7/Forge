-- Phase 2 developer experience: settings, build artifacts, rollback pointer.
--
-- root_directory: subdirectory inside the cloned tree that detect/build use.
--   Validated in Go (relative, no ".."). A malicious value must not let the
--   worker walk outside the clone; ConfineRoot enforces that again at run time.
--
-- health_path: HTTP path the worker GETs once at go-live. Default "/".
--   After LIVE, the API/dashboard uses docker inspect, not HTTP, so
--   polling the dashboard does not flood the app's access logs.
--   Must start with "/" and must not look like a URL (no "://") so it cannot
--   be turned into an SSRF target. The probe always hits 127.0.0.1:{port}.
--
-- local_host: optional DNS-ish slug. When set, Caddy also routes
--   http://{slug}.localhost:{proxyPort}/ in addition to /d/{deployment_id}/.
--   This is NOT a public custom domain and NOT TLS (those are later phases).
--   Unique among non-empty values so two apps cannot claim the same Host.
--
-- deployments.image_name: the docker tag the worker built (or copied on
--   rollback). Clients never supply this. Rollback reuses a prior image
--   instead of cloning and building again.
--
-- deployments.build_log: truncated docker build output. Survives after the
--   container is gone, unlike `docker logs`. Never treat this as a secret
--   store; still sanitize NULs and cap length in Go before UPDATE.
--
-- deployments.rollback_of: the source row if this attempt is a rollback.
--   ON DELETE SET NULL so deleting history cannot break the newer row.

ALTER TABLE applications
    ADD COLUMN IF NOT EXISTS root_directory TEXT NOT NULL DEFAULT '.'
        CHECK (char_length(root_directory) BETWEEN 1 AND 200),
    ADD COLUMN IF NOT EXISTS health_path TEXT NOT NULL DEFAULT '/'
        CHECK (char_length(health_path) BETWEEN 1 AND 200),
    ADD COLUMN IF NOT EXISTS local_host TEXT NOT NULL DEFAULT ''
        CHECK (char_length(local_host) <= 63);

CREATE UNIQUE INDEX IF NOT EXISTS applications_local_host_idx
    ON applications (local_host)
    WHERE local_host <> '';

ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS image_name TEXT NOT NULL DEFAULT ''
        CHECK (char_length(image_name) <= 200),
    ADD COLUMN IF NOT EXISTS build_log TEXT NOT NULL DEFAULT ''
        CHECK (char_length(build_log) <= 32768),
    ADD COLUMN IF NOT EXISTS rollback_of UUID REFERENCES deployments (id) ON DELETE SET NULL;
