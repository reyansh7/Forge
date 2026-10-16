-- Worker ownership for an in-flight deployment.
--
-- active_job_id (0014) says which Redis message may start the attempt.
-- It cannot tell that the worker died after BLPOP. lease_until is that
-- signal: the worker renews it while the pipeline runs. When it is in
-- the past, the API sweeper may re-queue an early stage or mark the
-- attempt failed. It must not mark a different row LIVE.
--
-- recovery_count bounds automatic re-queues. Exhaustion becomes a
-- visible failed row, not a loop.

ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS lease_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS recovery_count INTEGER NOT NULL DEFAULT 0;
