-- Phase 7 increment 7.7: GitHub OAuth, repo/branch binding, webhook
-- dedupe, and commit metadata on deployments.
--
-- Tokens and webhook secrets are stored as text so the process can
-- Seal them (enc:v1:) with the same AES-GCM data key as env vars.
-- The dashboard never reads those columns; HTTP maps them off.
--
-- Unique (repo_full_name, branch) is the routing key: a push names
-- owner/repo and refs/heads/<branch>. Two apps cannot claim the
-- same pair or webhook delivery would be ambiguous.
--
-- github_webhook_deliveries is X-GitHub-Delivery idempotency. GitHub
-- retries; inserting the same id twice must be a no-op, not a second
-- TypeDeploy job.
--
-- commit_* on deployments is source metadata for auto-deploy. Manual
-- deploys leave them empty. Rollback copies the source row's values.

CREATE TABLE IF NOT EXISTS github_accounts (
    user_id UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    github_user_id BIGINT NOT NULL,
    login TEXT NOT NULL
        CHECK (char_length(login) BETWEEN 1 AND 64),
    access_token TEXT NOT NULL
        CHECK (char_length(access_token) BETWEEN 1 AND 4096),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS github_accounts_github_user
    ON github_accounts (github_user_id);

CREATE TABLE IF NOT EXISTS github_oauth_states (
    nonce TEXT PRIMARY KEY
        CHECK (char_length(nonce) BETWEEN 16 AND 64),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    return_to TEXT NOT NULL DEFAULT '/'
        CHECK (char_length(return_to) BETWEEN 1 AND 256),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS application_github (
    application_id UUID PRIMARY KEY REFERENCES applications (id) ON DELETE CASCADE,
    owner_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    repo_full_name TEXT NOT NULL
        CHECK (char_length(repo_full_name) BETWEEN 3 AND 200),
    branch TEXT NOT NULL
        CHECK (char_length(branch) BETWEEN 1 AND 255),
    environment TEXT NOT NULL DEFAULT 'production'
        CHECK (environment IN ('production', 'preview')),
    webhook_id BIGINT,
    webhook_secret TEXT NOT NULL
        CHECK (char_length(webhook_secret) BETWEEN 16 AND 4096),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS application_github_repo_branch
    ON application_github (lower(repo_full_name), lower(branch));

CREATE INDEX IF NOT EXISTS application_github_owner
    ON application_github (owner_id);

CREATE TABLE IF NOT EXISTS github_webhook_deliveries (
    delivery_id TEXT PRIMARY KEY
        CHECK (char_length(delivery_id) BETWEEN 8 AND 128),
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE deployments
    ADD COLUMN IF NOT EXISTS commit_sha TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS commit_message TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS commit_author TEXT NOT NULL DEFAULT '';
