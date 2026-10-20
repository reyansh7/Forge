# Control-plane schema upgrades

Forge applies SQL from `internal/store/migrations/` at process start (`Postgres.Migrate`). Filenames are the versions. `schema_migrations` records which files have run.

## Rules

1. **Add a new file.** Do not edit a migration that already shipped on a machine you care about. `Migrate` skips applied names; changing an old file will not re-run it and will diverge from a fresh database.
2. **Idempotent where possible.** Prefer `IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS` so a retry after a crash is safe.
3. **No destructive default.** Do not `DROP COLUMN` in the same phase you stop writing it. Add the replacement, dual-write or migrate rows, drop later.
4. **Back up first.** `go run ./cmd/backup` before a schema you have not applied on that node. See `docs/DISASTER_RECOVERY.md`.
5. **API and worker share migrations.** Both call `Migrate` so either process can start first. Do not give them different migration sets.

## How to add a version

1. Create `internal/store/migrations/NNNN_description.sql` with the next integer prefix.
2. Describe *why* in a SQL comment (teaching, not `create table`).
3. Run `go test ./internal/store/...` against Compose Postgres.
4. Note the new filename in the phase docs if behavior changed.

`GET /operator/status` (session required) lists applied filenames so you can see drift. Phase 6 workers share the same migration set; they are not independent schemas.

`0008_nodes.sql` adds `nodes` and `deployments.node_id`. Restart API and worker so both migrate. Existing queued jobs on the old `forge:jobs` list are not consumed — re-enqueue after upgrade.

`0009_application_links.sql` adds same-project service edges. Restart API and worker. Links apply on the **next** deploy, not to a live container.

`0010_application_volumes.sql` adds persistent volume declarations. Restart API and worker. Mounts apply on the **next** deploy. Deleting a volume (or the application) best-effort `docker volume rm`s on this node; a remote worker may leave an orphan named volume.

`0011_deployment_environment.sql` adds `deployments.environment` (`preview`|`production`) and unique LIVE per `(application_id, environment)`. Restart API and worker. Existing rows default to production; `{slug}.localhost` is unchanged. Preview uses `{slug}-preview.localhost`. Phase 7.5 mounts named volumes on preview under `forgevol_<app>_preview_<name>` (production names stay `forgevol_<app>_<name>` so existing data is not orphaned). No new migration for 7.5.

`0012_application_domains.sql` adds public hostnames. Restart API and worker. Create a TXT record at `_forge-challenge.<hostname>` then POST verify. Caddy ACME needs :80/:443 reachable; Compose still publishes loopback. Certificates persist in the `forge_caddy_data` volume.

`0013_github_oauth.sql` adds GitHub accounts, repo/branch bindings, webhook delivery dedupe, and `deployments.commit_*`. Restart API and worker. Set `FORGE_GITHUB_CLIENT_ID` / `FORGE_GITHUB_CLIENT_SECRET`. Tokens are sealed with the same data key as env vars. Do not commit those secrets.

`0024_webhook_delivery_state.sql` adds `state`, `attempt`, `deployment_id`, and `updated_at` on `github_webhook_deliveries`. Restart the API. Existing delivery rows stay `queued`, so an old id is not deployed again. A new push stays `pending` until Redis accepts the job. A failed enqueue stays `failed` and the same GitHub delivery may retry that deployment. Do not delete the row to force a retry.

Public webhook address, set before saving a repository binding:

```text
FORGE_WEBHOOK_BASE=https://your-public-host/forge-api
```

Use that form when the public host is the dashboard and Next forwards `/forge-api` to the API. If the public host reaches the API directly, omit `/forge-api`. Restart the API, then save the binding again from the application GitHub card. Forge registers the GitHub hook only when that URL is not loopback, a `.local` name, or a private IP. It does not open a tunnel or a cloud port for you. Until `FORGE_WEBHOOK_BASE` is a reachable public origin, the card shows `webhook: false` and the API warning, and a GitHub push does not create a deployment. `FORGE_PUBLIC_BASE` is only the OAuth redirect. `FORGE_GITHUB_ALLOWED_USER_IDS` still gates login and connect. The webhook itself is trusted only by HMAC.

`0014_deployment_active_job.sql` adds `deployments.active_job_id`. Restart API and worker. Existing queued rows keep an empty id and still run. New deploys and **Re-queue** stamp the id before `RPUSH`.

`0015_deployment_lease.sql` adds `lease_until` and `recovery_count`. Restart API and worker together. The worker renews the lease while a deploy runs. The API and the worker sweep expired leases. A worker restart expires only that node's in-flight leases; it does not mark them failed itself. See `docs/DEPLOYMENT_RECOVERY.md`.

`0016_github_login.sql` allows `github_oauth_states.user_id` to be NULL. Restart the API. NULL is Sign in with GitHub. A non-NULL row is still "connect this already-signed-in operator".

`0017_platform_hostnames.sql` adds `application_domains.origin` (`custom`|`platform`). Restart the API and the worker. Existing rows stay `custom`. Platform rows appear only when `FORGE_APPS_DOMAIN` is set. Set `FORGE_GITHUB_ALLOWED_USER_IDS` before relying on GitHub login; an empty list refuses it.

## Env encryption and upgrades

Phase 5 seals new env values as `enc:v1:...`. Rows without that prefix still `Open` as plaintext so a restored Phase 3 dump keeps working after you set the data key. Re-saving an env var re-seals it.

Losing `.forge/data.key` is not a schema problem. It is a secret-loss problem. A new key cannot read old ciphertext.

## What this is not

- Not a rolling upgrade of two API processes (still one control plane).
- Not live container migration when a worker dies.
- Not an automatic down-migration.
- Not permission to rewrite history of applied files on a live database.
