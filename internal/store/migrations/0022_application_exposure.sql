-- Explicit public or private exposure from forge.yaml.
--
-- Empty keeps the role default: a frontend is routed, a backend,
-- worker, or ML service is not, unless local_host is set. "private"
-- keeps the service off Caddy even when its name looks public.

ALTER TABLE applications
    ADD COLUMN IF NOT EXISTS exposure TEXT NOT NULL DEFAULT ''
        CHECK (exposure IN ('', 'public', 'private'));
