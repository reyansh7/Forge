-- A GitHub delivery is not finished when the row is inserted.
--
-- pending: one request owns the attempt. A concurrent retry is told
-- to come back (HTTP 503), not that the push was deployed.
-- failed: enqueue did not accept a job. The same X-GitHub-Delivery
-- may try again and must reuse the deployment row when one exists.
-- queued: a job was accepted, or this push was intentionally skipped.
-- A later delivery of the same id must not start a second release.
--
-- Existing rows were inserted by the old "claim forever" code. They
-- default to queued so a replay does not deploy them a second time.

ALTER TABLE github_webhook_deliveries
    ADD COLUMN IF NOT EXISTS state TEXT NOT NULL DEFAULT 'queued',
    ADD COLUMN IF NOT EXISTS attempt INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS deployment_id UUID,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'github_webhook_deliveries_state_check'
    ) THEN
        ALTER TABLE github_webhook_deliveries
            ADD CONSTRAINT github_webhook_deliveries_state_check
            CHECK (state IN ('pending', 'queued', 'failed'));
    END IF;
END $$;
