-- Phase 7 increment 7.6: public custom domains.
--
-- Postgres stores the hostname the operator claims. Caddy routes that
-- Host only after DNS TXT proves they control the name, then obtains
-- a certificate with ACME. Unique hostname is the theft invariant:
-- application B cannot attach a name already claimed by A, including
-- a pending row (squatting is first-writer, not silent steal).
-- Environment scopes which LIVE container receives the Host.
-- .localhost slugs stay on applications.local_host; they are not rows
-- here and must be rejected as hostnames so ACME never runs on loopback.
CREATE TABLE IF NOT EXISTS application_domains (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    hostname TEXT NOT NULL
        CHECK (char_length(hostname) BETWEEN 4 AND 253)
        CHECK (hostname = lower(hostname))
        CHECK (hostname NOT LIKE '%:')
        CHECK (position('/' IN hostname) = 0),
    environment TEXT NOT NULL DEFAULT 'production'
        CHECK (environment IN ('production', 'preview')),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'active')),
    token TEXT NOT NULL
        CHECK (char_length(token) BETWEEN 16 AND 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS application_domains_hostname
    ON application_domains (hostname);

CREATE INDEX IF NOT EXISTS application_domains_application
    ON application_domains (application_id);
