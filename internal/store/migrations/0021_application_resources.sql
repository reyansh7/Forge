-- Per-application container budget and readiness timeout.
--
-- Zero means the historical default (256 MiB, 500 milli-CPU, 256
-- PIDs, 45s health). The columns do not raise the workspace quota
-- and do not change a container that is already running. Go rejects
-- values outside the ceiling before they reach docker run.

ALTER TABLE applications
    ADD COLUMN IF NOT EXISTS memory_mb INTEGER NOT NULL DEFAULT 0
        CHECK (memory_mb = 0 OR memory_mb BETWEEN 64 AND 2048),
    ADD COLUMN IF NOT EXISTS cpu_millis INTEGER NOT NULL DEFAULT 0
        CHECK (cpu_millis = 0 OR cpu_millis BETWEEN 100 AND 2000),
    ADD COLUMN IF NOT EXISTS pids_limit INTEGER NOT NULL DEFAULT 0
        CHECK (pids_limit = 0 OR pids_limit BETWEEN 32 AND 1024),
    ADD COLUMN IF NOT EXISTS health_timeout_seconds INTEGER NOT NULL DEFAULT 0
        CHECK (health_timeout_seconds = 0 OR health_timeout_seconds BETWEEN 5 AND 180);
