-- Re-queue of a deployment that is still queued.
--
-- Redis LISTs drop jobs on restart. The deployment row stays queued and
-- blocks a second Deploy. POST /deployments/{id}/retry writes a new
-- active_job_id, then RPUSHes. The worker runs the job only when the
-- id matches, so a copy still sitting on the list is a no-op.
--
-- Empty string is the pre-claim default. Those rows still run, so a
-- deploy queued before this migration is not stranded.

ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS active_job_id TEXT NOT NULL DEFAULT '';
