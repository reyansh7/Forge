# Forge disaster recovery (single node)

This is the Phase 5 runbook for **one machine**. Multi-region DR is Phase 10. Do not treat this file as permission to bind the API on the public Internet.

Backups are **secrets**. They contain user rows and sealed env values. Keep them off git. Do not paste dump contents into chat.

## What is durable

| Store | Role | If it disappears |
|-------|------|------------------|
| PostgreSQL | Users, sessions, projects, apps, env (sealed), deployments, audit, schema_migrations | Control plane is gone until restore |
| Redis LIST `forge:jobs` | Transient queue only | Queued jobs that were never popped are lost. Rows in `deployments` stay. Re-queue a deploy from the dashboard or API |
| `.forge/data.key` | AES-256-GCM key for env cells | Sealed env cannot be opened. A new key does **not** decrypt old rows |
| Docker images / containers | Workload artifacts | Rebuild from git. Rollback still works if `image_name` is on the row and the image is still on the host |
| Caddy in-memory config | LIVE routes | Worker start re-applies LIVE rows. No Caddy dump required |

## Backup

From the repo root (same `.env` as the API):

```powershell
go run ./cmd/backup
```

Writes `backups/forge-<utc>.sql` (mode 0600 when the OS honors it). Optional `-out path`.

Needs `pg_dump` on `PATH`, or Docker Compose with the `postgres` service up (`docker compose exec`).

## Restore (recovery instance)

Restore is destructive to the target database. Use a throwaway database first.

```powershell
# Example: dump, then restore into the same local Compose DB only when you mean it
go run ./cmd/backup -restore backups/forge-YYYYMMDD-HHMMSS.sql
```

After restore:

1. Confirm `.forge/data.key` (or `FORGE_DATA_KEY`) is the **same** key used when the dump was taken.
2. Restart `cmd/api` and `cmd/worker` so they migrate (idempotent) and the worker reloads Caddy from LIVE rows.
3. Redis need not be restored. In-flight `queued` rows will not have a LIST entry — enqueue those deployments again.
4. `GET /health` then `GET /operator/status` (session required).

## Restore drill (exit criterion)

A dump that contains `schema_migrations` (or `CREATE`) is the automated check (`go test ./internal/backup/...` when Postgres is reachable).

A manual drill:

1. `go run ./cmd/backup -out backups/drill.sql`
2. Confirm the file is not empty and is not committed.
3. On a **copy** of the database (or after you accept data loss), `go run ./cmd/backup -restore backups/drill.sql`
4. Log in and list projects you expect.

Do not run restore against a database you are not willing to overwrite.

## Redis-loss behavior

A Redis restart empties `forge:jobs`. PostgreSQL still has `queued` / in-progress rows. The worker will not see those jobs until someone enqueues again. That is accepted in this phase. Do not promote Redis to a durable workflow engine.

## TLS and bind after recovery

- Default API bind remains `127.0.0.1:8080`.
- `0.0.0.0` requires `FORGE_ALLOW_PUBLIC_BIND=1`. Auth is still required.
- App HTTPS is Caddy `:443` published as `127.0.0.1:9443` with `tls internal` (local CA, browser warning). ACME / public DNS waits for Phase 7.
