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

## Env encryption and upgrades

Phase 5 seals new env values as `enc:v1:...`. Rows without that prefix still `Open` as plaintext so a restored Phase 3 dump keeps working after you set the data key. Re-saving an env var re-seals it.

Losing `.forge/data.key` is not a schema problem. It is a secret-loss problem. A new key cannot read old ciphertext.

## What this is not

- Not a rolling upgrade of two API processes (still one control plane).
- Not live container migration when a worker dies.
- Not an automatic down-migration.
- Not permission to rewrite history of applied files on a live database.
