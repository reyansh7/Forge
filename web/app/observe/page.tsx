"use client";

import { FormEvent, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import {
  createNode,
  getHealth,
  getMetrics,
  listNodes,
  type Health,
  type ObserveMetrics,
  type ObserveService,
  type WorkerNode,
} from "../api";
import {
  containerPhrase,
  counterWindow,
  healthLabel,
  healthPill,
  nextAction,
  observeView,
  platformSummary,
} from "../observe-state";
import { formatDuration, statusLabel, when } from "../status";

export default function ObservePage() {
  const [m, setM] = useState<ObserveMetrics | null>(null);
  const [nodes, setNodes] = useState<WorkerNode[]>([]);
  const [platform, setPlatform] = useState<Health | null>(null);
  const [error, setError] = useState("");
  const [platformError, setPlatformError] = useState("");
  const [name, setName] = useState("");
  const [advertise, setAdvertise] = useState("");
  const [busy, setBusy] = useState(false);
  const [shownToken, setShownToken] = useState("");
  const [shownName, setShownName] = useState("");

  const load = useCallback(async () => {
    try {
      const [next, list] = await Promise.all([getMetrics(), listNodes()]);
      setM(next);
      setNodes(list);
      setError("");
    } catch (e) {
      // Keep the last good payload. A failed refresh is stale data,
      // not a license to draw zeros.
      setError(e instanceof Error ? e.message : "metrics failed");
    }
    try {
      setPlatform(await getHealth());
      setPlatformError("");
    } catch (e) {
      setPlatformError(e instanceof Error ? e.message : "platform health failed");
    }
  }, []);

  useEffect(() => {
    void load();
    const t = window.setInterval(() => void load(), 4000);
    return () => window.clearInterval(t);
  }, [load]);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const n = await createNode(name, advertise);
      setShownToken(n.join_token || "");
      setShownName(n.name);
      setName("");
      setAdvertise("");
      setNodes(await listNodes());
    } catch (err) {
      setError(err instanceof Error ? err.message : "create node failed");
    } finally {
      setBusy(false);
    }
  }

  const view = observeView(m !== null, error !== "");
  const services = m?.services;

  return (
    <>
      <section className="hero">
        <div className="observe-head">
          <div>
            <p className="crumb">
              <Link href="/">Projects</Link>
              {" / "}
              Observe
            </p>
            <h1>Observe</h1>
            <p className="lede">
              Whether Forge is healthy, which deployed applications need attention, and what to open next.
            </p>
          </div>
          <div className="observe-fresh">
            <p className="hint" aria-live="polite">
              {freshness(m, view)}
            </p>
            <button className="btn-ghost" type="button" onClick={() => void load()}>
              Refresh
            </button>
          </div>
        </div>
      </section>

      {view === "loading" ? <Loading /> : null}
      {view === "error" ? (
        <section className="card">
          <h2>Metrics unavailable</h2>
          <p className="error">{error}</p>
          <p className="hint">Nothing below is filled with placeholder counts. Refresh retries the same session-gated requests.</p>
          <button className="btn-primary" type="button" onClick={() => void load()}>
            Try again
          </button>
        </section>
      ) : null}
      {view === "stale" ? (
        <p className="error" role="status">
          Showing the last successful read. The latest refresh failed: {error}
        </p>
      ) : null}

      {m ? (
        <div className="observe-layout">
          <Platform m={m} platform={platform} platformError={platformError} services={services} />
          <Applications services={services} />
          <DeploymentActivity m={m} />
          <Workers m={m} nodes={nodes} />
          <Resources services={services} />
        </div>
      ) : null}

      <details className="card" style={{ marginTop: "0.85rem" }}>
        <summary>Register another worker</summary>
        <form onSubmit={(e) => void onCreate(e)}>
          <div className="form-grid">
            <div>
              <label htmlFor="node-name">Name</label>
              <input id="node-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="lab2" required />
            </div>
            <div>
              <label htmlFor="node-host">Advertise host (optional)</label>
              <input
                id="node-host"
                value={advertise}
                onChange={(e) => setAdvertise(e.target.value)}
                placeholder="leave empty on this machine"
              />
            </div>
          </div>
          <div className="actions">
            <button className="btn-primary" type="submit" disabled={busy}>
              {busy ? "Creating…" : "Create node"}
            </button>
          </div>
          {shownToken ? (
            <p className="hint">
              Join token for <strong>{shownName}</strong>, shown once. Put it in that worker&apos;s environment as{" "}
              <code>FORGE_NODE_TOKEN</code> and set <code>FORGE_NODE_NAME</code> to the name above. Do not commit it or paste
              it into a deployment.
              <br />
              <code className="token">{shownToken}</code>
            </p>
          ) : null}
        </form>
      </details>
    </>
  );
}

function Platform({
  m,
  platform,
  platformError,
  services,
}: {
  m: ObserveMetrics;
  platform: Health | null;
  platformError: string;
  services: ObserveService[] | undefined;
}) {
  const summary =
    services === undefined
      ? {
          tone: "unknown",
          label: "Application list unavailable",
          detail:
            "This API response has no per-application health. Restart the API process that is already running so it loads the current metrics handler. The counts below are still real.",
        }
      : platformSummary(services.map((row) => row.health));
  return (
    <section className="card">
      <h2>Platform</h2>
      <p className={`observe-summary sev-${summary.tone}`}>
        <span className={healthPill(summary.tone)}>{summary.label}</span>
        <span>{summary.detail}</span>
      </p>
      <div className="stat-grid">
        <Stat
          label="Postgres"
          value={platform ? platform.postgres : "unavailable"}
          note={
            platform
              ? "Control-plane database ping from GET /health. This is Forge's own database, not an application's."
              : platformError || "The control-plane health check did not return."
          }
          tone={platform?.postgres === "ok" ? "healthy" : "unknown"}
        />
        <Stat
          label="Redis"
          value={platform ? platform.redis : "unavailable"}
          note={
            platform
              ? "Control-plane queue ping from GET /health. This does not measure how many jobs are waiting."
              : platformError || "The control-plane health check did not return."
          }
          tone={platform?.redis === "ok" ? "healthy" : "unknown"}
        />
        <Stat
          label="HTTP requests"
          value={String(m.http_requests_total)}
          note={counterWindow(m.started_at)}
          tone="unknown"
        />
        <Stat
          label="HTTP 5xx"
          value={String(m.http_errors_total)}
          note={counterWindow(m.started_at)}
          tone={m.http_errors_total > 0 ? "degraded" : "unknown"}
        />
      </div>
    </section>
  );
}

function Applications({ services }: { services: ObserveService[] | undefined }) {
  return (
    <section className="card">
      <h2>Applications</h2>
      <p className="hint">
        One row is one application in one environment. Attempts are deployment rows. The release is the live row when one
        exists, otherwise the latest attempt.
      </p>
      {services === undefined ? (
        <p className="empty">Per-application health is not in this response yet.</p>
      ) : services.length === 0 ? (
        <p className="empty">No applications yet. Open a project and add one.</p>
      ) : (
        <div className="observe-services">
          {services.map((row) => (
            <ServiceRow key={`${row.application_id}:${row.environment}`} row={row} />
          ))}
        </div>
      )}
    </section>
  );
}

function ServiceRow({ row }: { row: ObserveService }) {
  const appHref = `/applications/${row.application_id}`;
  return (
    <article className={`observe-service sev-${row.health || "unknown"}`}>
      <div className="observe-service-top">
        <div>
          <strong>{row.application_name}</strong>
          <p className="empty">
            {row.environment || "production"}
            {row.release_sha ? ` · ${row.release_sha.slice(0, 7)}` : ""}
            {row.release_status ? ` · ${statusLabel(row.release_status)}` : ""}
          </p>
        </div>
        <span className={healthPill(row.health)} title={row.health_reason}>
          {healthLabel(row.health)}
        </span>
      </div>
      <p className="hint">{row.health_reason}</p>
      <div className="observe-meta">
        <Field label="Container" value={containerPhrase(row.container)} note={row.health_reason} />
        <Field
          label="Last status write"
          value={row.status_at ? when(row.status_at) : "unavailable"}
          note="When this deployment row last changed status. Not a container start clock."
        />
        <Field label="Uptime" value={labelUnavailable(row.uptime)} note={row.uptime_reason} />
        <Field label="Restarts" value={labelUnavailable(row.restart_count)} note={row.restart_count_reason} />
        <Field label="Resources" value={labelUnavailable(row.resources)} note={row.resources_reason} />
        <Field
          label="Attempts"
          value={String(row.attempts)}
          note="Deployment rows for this application and environment, including retries."
        />
      </div>
      {row.failed_reason ? (
        <p className="error">
          {row.failed_stage ? `Failed during ${row.failed_stage.replaceAll("_", " ")}. ` : ""}
          {row.failed_reason}
        </p>
      ) : null}
      <p className="hint">{nextAction(row)}</p>
      <div className="observe-actions">
        <Link href={`${appHref}#logs`}>Logs</Link>
        <Link href={`${appHref}#deployments`}>Deployments</Link>
        <Link href={appHref}>Application</Link>
        {row.project_id ? <Link href={`/projects/${row.project_id}`}>Project</Link> : null}
      </div>
    </article>
  );
}

function DeploymentActivity({ m }: { m: ObserveMetrics }) {
  const unique = m.applications_unique;
  return (
    <section className="card">
      <h2>Deployment activity</h2>
      <p className="hint">
        Counts are rows in your account. A failed attempt does not erase an older live release.
        {m.collected_at ? ` Collected ${when(m.collected_at)}.` : ""}
      </p>
      <div className="stat-grid">
        <Stat
          label="Attempts"
          value={String(m.deployments_total)}
          note="Every deployment row, including retries. Not the number of applications."
        />
        <Stat
          label="Applications"
          value={unique === undefined ? "unavailable" : String(unique)}
          note="Distinct applications in this response. One application can have a production row and a preview row."
        />
        <Stat
          label="Live releases"
          value={String(m.deployments_live)}
          note="Rows whose status is live. One application has at most one live row per environment."
          tone={m.deployments_live > 0 ? "healthy" : "unknown"}
        />
        <Stat
          label="Failed attempts"
          value={String(m.deployments_failed)}
          note="Rows whose status is failed. Open the application if a live release is still serving."
          tone={m.deployments_failed > 0 ? "unhealthy" : "healthy"}
        />
        <Stat
          label="In progress"
          value={m.deployments_in_progress === undefined ? "unavailable" : String(m.deployments_in_progress)}
          note="Attempts currently queued, building, or waiting on a health check. This is not the Redis list length."
          tone={m.deployments_in_progress ? "degraded" : "unknown"}
        />
        <Stat
          label="Last finished duration"
          value={m.last_deploy_duration_ms ? formatDuration(m.last_deploy_duration_ms) : "unavailable"}
          note="Create-to-status-write time of your most recently finished attempt. Not an average."
        />
      </div>
    </section>
  );
}

function Workers({ m, nodes }: { m: ObserveMetrics; nodes: WorkerNode[] }) {
  return (
    <section className="card">
      <h2>Workers and queue</h2>
      <div className="stat-grid">
        <Stat
          label="Workers ready"
          value={String(m.nodes_ready)}
          note="Nodes currently sending heartbeats. A ready worker can claim a deployment."
          tone={m.nodes_ready > 0 ? "healthy" : "unknown"}
        />
        <Stat
          label="Workers stale"
          value={String(m.nodes_dead)}
          note="Nodes marked dead after missed heartbeats. Deployments and volumes are not deleted."
          tone={m.nodes_dead > 0 ? "stale" : "healthy"}
        />
        <Stat
          label="Jobs accepted"
          value={String(m.deploys_queued_total)}
          note={`${counterWindow(m.started_at)} This is not how many jobs are waiting.`}
        />
        <Stat
          label="Queue depth"
          value={m.queue_depth === "unavailable" || !m.queue_depth ? "unavailable" : m.queue_depth}
          note={m.queue_depth_reason || "Current Redis list length is not read."}
          tone="unknown"
        />
      </div>
      {nodes.length === 0 ? (
        <p className="empty">No workers have reported yet. A deployment stays queued until one heartbeats.</p>
      ) : (
        <div className="project-list">
          {nodes.map((n) => (
            <div className="project-row" key={n.id}>
              <div>
                <strong>{n.name}</strong>
                <p className="empty">
                  {nodeStatusLabel(n.status)}
                  {n.name === "local" ? " · this machine" : " · registered"}
                  {` · ${n.in_progress} in progress`}
                  {n.last_seen_at ? ` · last heartbeat ${when(n.last_seen_at)}` : " · no heartbeat yet"}
                </p>
                {n.status === "dead" ? (
                  <p className="hint">
                    This worker stopped heartbeating. Containers it already started are unchanged. Restart that worker
                    process. Move a workload only by stopping the application and deploying it again.
                  </p>
                ) : null}
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

function Resources({ services }: { services: ObserveService[] | undefined }) {
  const sample = services?.[0];
  return (
    <section className="card">
      <h2>Runtime resources</h2>
      <p className="hint">These are not measured. A dash would look like zero, so the value stays unavailable.</p>
      <div className="stat-grid">
        <Stat
          label="CPU and memory"
          value="unavailable"
          note={
            sample?.resources_reason ||
            "CPU and memory are not collected. A docker stats or cgroup read would be required, and this API does not call either."
          }
        />
        <Stat
          label="Restart count"
          value="unavailable"
          note={
            sample?.restart_count_reason ||
            "Docker inspect RestartCount is not read. The runtime check only reports whether the container is running."
          }
        />
        <Stat
          label="Containers probed"
          value={services === undefined ? "unavailable" : "see applications"}
          note="The probe asks Docker if the container is running. It does not read CPU, memory, or restart count. At most 20 live containers are inspected per refresh."
        />
      </div>
    </section>
  );
}

function Loading() {
  return (
    <div className="observe-layout" aria-busy="true" aria-live="polite">
      <p className="hint">Loading metrics…</p>
      <div className="observe-skeleton" />
      <div className="observe-skeleton" />
      <div className="observe-skeleton" />
    </div>
  );
}

function Stat({ label, value, note, tone }: { label: string; value: string; note: string; tone?: string }) {
  return (
    <div className={tone ? `stat sev-${tone}` : "stat"} title={note}>
      <span className="stat-label">{label}</span>
      <span className="stat-value">{value}</span>
      <span className="stat-note">{note}</span>
    </div>
  );
}

function Field({ label, value, note }: { label: string; value: string; note?: string }) {
  return (
    <div title={note}>
      <span className="observe-k">{label}</span>
      <span>{value}</span>
    </div>
  );
}

function freshness(m: ObserveMetrics | null, view: string): string {
  if (view === "loading") {
    return "Waiting for the first response.";
  }
  if (view === "error") {
    return "No successful read yet.";
  }
  if (!m?.collected_at) {
    return "This response did not include a collection time. Refresh still polls every 4 seconds.";
  }
  return `Updated ${when(m.collected_at)}. Refreshes every 4 seconds.`;
}

function labelUnavailable(value: string | undefined): string {
  if (!value || value === "unavailable") {
    return "unavailable";
  }
  return value;
}

function nodeStatusLabel(status: string): string {
  if (status === "ready") {
    return "Ready";
  }
  if (status === "dead") {
    return "Stale";
  }
  return status || "Unknown";
}
