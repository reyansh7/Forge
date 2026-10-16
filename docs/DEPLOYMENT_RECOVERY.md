# Deployment recovery

This is how a Forge attempt fails closed. It is not high availability and it is not an AWS deploy.

## State machine

```text
queued → detecting → building → provisioning → deploying → health_check → live
                  ↘ failed (failed_stage names the step)
                  ↘ stopped (operator Stop, or replaced after a healthy cutover)
```

Invariants:

- One application has at most one `live` row per environment (`production` or `preview`).
- A second Deploy is rejected while a row for that environment is still in progress.
- `active_job_id` is the only Redis message allowed to start that row. A re-queue replaces it. An older message returns without building.
- `lease_until` is worker ownership after the message is popped. The worker renews it about every 30 seconds for 90 seconds. State writes from that pipeline include the job id, the node id, and an unexpired lease.
- Cutover to `live` is one database transaction: the previous live row becomes `stopped`, then the new row becomes `live`. If the new row no longer owns the lease, the transaction rolls back and the previous live row stays live.
- The sweeper never updates a row it did not name. Stopping a container uses that attempt's container name, not the previous live container.

## Crash windows

| Window | What remains | Recovery |
|---|---|---|
| Row inserted, process dies before RPUSH | `queued`, no lease | Sweeper waits out the grace period, assigns a new job id, pushes again. Manual **Re-queue** does the same immediately. |
| Message sitting in Redis, worker dies before pop | `queued`, no lease | The message is still valid if `active_job_id` matches. If Redis was flushed, the grace path above applies. |
| Two workers pop the same message | both see `queued` | `AcquireLease` is one UPDATE. One row changes. The other gets lease lost and does not build. |
| Worker dies during detect or build | expired lease, no cutover | Sweeper re-queues the same row up to 2 times, or marks it `failed` when the count is exhausted. |
| Worker dies after `docker run`, before health passes | expired lease, this attempt's container may be running | Sweeper marks **this** row `failed` and stops **this** container. The previous live release is not updated. |
| Health fails | previous live still serving | Existing rolling deploy. The new container is removed. Caddy is not switched. |
| Worker dies after cutover commits | new row is `live` | The new release is the current one. Stopping the previous container is best-effort after commit. A leftover previous container is not routed once Caddy has switched. |

Automatic re-queue does not cover `provisioning`, `deploying`, or `health_check`. Those stages may already have a container. Running them again could start a second one. They become `failed` with the previous live release left in place. The operator Deploys again for a new attempt.

`recovery_count` stops at 2 automatic re-queues. The next expiry is `failed`, not a loop.

## What this does not do

- It does not move a running container to another machine.
- It does not make Redis durable. Postgres records the attempt; Redis only wakes a worker.
- It does not add a second control plane. One API and one Postgres remain a single failure domain.
- Docker resource limits (`--memory 256m`, `--cpus 0.5`, `--pids-limit 256`, `--cap-drop ALL`, no socket, no host bind) already apply at `docker run`. They are not a VM boundary. The build itself is `docker build` under the worker context, so a cancelled lease stops the build process. A container that shares the host kernel is not a separate virtual machine.
