-- At most one LIVE deployment per application.
--
-- Deploy used to start a new container and only then stop the previous
-- LIVE row. Two clicks could leave two LIVE rows and two containers.
-- This unique index is the database invariant; the API also refuses a
-- new deploy while a container is still running.
--
-- Extra LIVE rows from before this increment are demoted (newest kept)
-- so the index can be created. Orphan containers are stopped on the
-- next Stop/Deploy of that application or when the worker reconciles.

WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY application_id
               ORDER BY created_at DESC
           ) AS rn
    FROM deployments
    WHERE status = 'live'
)
UPDATE deployments d
SET status = 'stopped',
    error_message = 'replaced: only one live deployment per application',
    updated_at = now()
FROM ranked r
WHERE d.id = r.id
  AND r.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS deployments_one_live_per_application
    ON deployments (application_id)
    WHERE status = 'live';
