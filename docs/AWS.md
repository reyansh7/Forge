# AWS setup for a personal Forge

This is the install for one EC2 host. It is not high availability, and applying it is the operator's step. Forge does not create the account or spend money from this repository.

Phase 8 in `docs/ROADMAP.md` stays future until you explicitly start it. The files under `deploy/aws/` are the readiness package: same API, worker, Postgres, Redis, Docker, and Caddy, on a host whose only public ports are 80 and 443.

## What stays private

| Port | Process | Bind |
|---|---|---|
| 5432 | Postgres | `127.0.0.1` inside the host |
| 6379 | Redis | `127.0.0.1` |
| 2019 | Caddy admin | `127.0.0.1` |
| 8080 | Go API | `127.0.0.1` |
| 3000 | Next.js dashboard | host only; Caddy reaches it as `host.docker.internal:3000` |
| 80, 443 | Caddy | public |

The security group in `deploy/aws/main.tf` allows 80 and 443 from the internet. It does not allow 5432, 6379, 2019, 3000, or 8080. SSH is closed unless you set both `admin_cidr` and `key_name`. Administration is SSM Session Manager.

There is no NAT gateway. One public subnet, one Elastic IP, a 30 GB encrypted gp3 volume. Default instance type is `t3.small`. `t3.micro` is cheaper and often runs out of memory during `docker build`; user-data adds 2 GB of swap either way.

## 1. DNS

Create these records at the Elastic IP from `terraform output public_ip`:

- `A` `*.forge.com` → that IP (replace `forge.com` with your zone)
- `A` `console.forge.com` → the same IP

`console` is a reserved slug, so an application cannot take it.

## 2. Host

```bash
cd deploy/aws
terraform init
terraform apply -var='apps_domain=forge.com'
```

Connect with SSM. Install is already in user-data (Docker, swap, log rotation). Copy the Forge repository onto the host. Do not commit `.env`.

```bash
docker compose -f docker-compose.yml -f docker-compose.aws.yml up -d
```

That publishes 80 and 443. Postgres, Redis, and Caddy admin stay on the loopback mappings from the base compose file.

## 3. Environment

Start from `.env.example`. On this host, change at least:

```text
FORGE_POSTGRES_PASSWORD=<long random password>
FORGE_DATABASE_URL=postgres://forge:<that password>@127.0.0.1:15432/forge?sslmode=disable
FORGE_GITHUB_ALLOWED_USER_IDS=<numeric GitHub user id>
FORGE_APPS_DOMAIN=forge.com
FORGE_CONTROL_HOST=console.forge.com
FORGE_PUBLIC_BASE=https://console.forge.com
FORGE_WEBHOOK_BASE=https://console.forge.com/forge-api
```

Leave `FORGE_API_ADDR=127.0.0.1:8080`. Do not set `FORGE_ALLOW_PUBLIC_BIND`. The dashboard rewrite in `web/next.config.ts` already targets `127.0.0.1:8080`, and Caddy sends the public hostname to Next.js.

The GitHub OAuth callback becomes `https://console.forge.com/forge-api/integrations/github/callback`. Update the GitHub OAuth app to that URL. Keep "Expire user access tokens" off.

`FORGE_GITHUB_ALLOWED_USER_IDS` is numeric ids, not login names. An empty value refuses every GitHub login and every repository connect. Webhooks are still accepted only when `X-Hub-Signature-256` matches.

## 4. Processes

On the host, with Go and Node installed:

```bash
go run ./cmd/api
go run ./cmd/worker
cd web && npm ci && npx next build && npx next start --hostname 0.0.0.0 --port 3000
```

`0.0.0.0` on Next is so the Caddy container can reach the dashboard through `host.docker.internal`. Port 3000 is not in the security group.

Restart the API and the worker after changing `.env`. The API applies migration `0017_platform_hostnames.sql` on startup.

## 5. Acceptance

1. Open `https://console.forge.com` and sign in with the allowlisted GitHub account. A different GitHub account is refused.
2. Create an application with slug `myapp`. The dashboard shows `https://myapp.forge.com`.
3. Connect the repository and branch. Push to that branch.
4. After the health check, `https://myapp.forge.com` serves the new release. A failed health check leaves the previous release in place.
5. A push to another branch does not deploy. A second delivery of the same webhook does not start a second release.

Let's Encrypt will not issue while the hostname still points somewhere else, or while ports 80 and 443 do not reach this Caddy. Leave `FORGE_APPS_DOMAIN` empty until the DNS records exist. With the zone set, a failed certificate request can fail the whole Caddy reload.

## 6. Backup

Dump from the host, then restore into a **different** database name. Restoring onto the live `FORGE_DATABASE_URL` replaces that database.

```bash
go run ./cmd/backup -out /var/backups/forge.sql
docker compose exec -T postgres createdb -U forge forge_restore
# Point a one-off restore at forge_restore, not the live URL.
```

Redis is not in the dump. Queued jobs can be lost; deployment rows are in Postgres. The 7.8 recovery sweeper re-queues early stages from those rows.

## What this host is not

One machine is one failure domain. It does not move containers to another node, it does not use ECS or EKS, and it does not issue a wildcard certificate (each app hostname gets its own HTTP-01 certificate through the existing Caddy path). Billing alarms and a second backup copy off the instance are operator tasks before calling the account ready.
