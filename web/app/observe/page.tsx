"use client";

import { FormEvent, useEffect, useState } from "react";
import Link from "next/link";
import { createNode, getMetrics, listNodes, type ObserveMetrics, type WorkerNode } from "../api";
import { formatDuration, when } from "../status";

export default function ObservePage() {
  const [m, setM] = useState<ObserveMetrics | null>(null);
  const [nodes, setNodes] = useState<WorkerNode[]>([]);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [advertise, setAdvertise] = useState("");
  const [busy, setBusy] = useState(false);
  const [shownToken, setShownToken] = useState("");
  const [shownName, setShownName] = useState("");

  useEffect(() => {
    let alive = true;
    async function load() {
      try {
        const [next, list] = await Promise.all([getMetrics(), listNodes()]);
        if (alive) {
          setM(next);
          setNodes(list);
          setError("");
        }
      } catch (e) {
        if (alive) {
          setError(e instanceof Error ? e.message : "metrics failed");
        }
      }
    }
    void load();
    const t = window.setInterval(() => void load(), 4000);
    return () => {
      alive = false;
      window.clearInterval(t);
    };
  }, []);

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

  return (
    <>
      <section className="hero">
        <p className="crumb">
          <Link href="/">Projects</Link>
          {" / "}
          Observe
        </p>
        <h1>Observe</h1>
        <p className="lede">
          Health of this control plane: requests handled by the running API, your deployments, and whether workers are reporting in.
        </p>
        {error ? <p className="error">{error}</p> : null}
      </section>

      <div className="stat-grid">
        <Stat label="HTTP requests" value={m ? String(m.http_requests_total) : "—"} note="This API process, since it started. Not stored." />
        <Stat label="HTTP 5xx responses" value={m ? String(m.http_errors_total) : "—"} note="This API process, since it started." />
        <Stat label="Queued deployments" value={m ? String(m.deploys_queued_total) : "—"} note="Count of deploy jobs accepted by this API process." />
        <Stat label="Deployments" value={m ? String(m.deployments_total) : "—"} note="Your rows in the database, all environments." />
        <Stat label="Live applications" value={m ? String(m.deployments_live) : "—"} note="Deployments whose status is live." />
        <Stat label="Failed deployments" value={m ? String(m.deployments_failed) : "—"} note="Deployments whose status is failed. A failed attempt can sit beside a live release." />
        <Stat
          label="Last deployment duration"
          value={m && m.last_deploy_duration_ms ? formatDuration(m.last_deploy_duration_ms) : "—"}
          note="Duration of your most recently finished attempt."
        />
        <Stat
          label="Containers running"
          value={m ? `${m.containers_running} running of ${m.containers_checked} probed` : "—"}
          note="Up to 20 of your live deployments are probed. Running means the probe found the container. 0 of 1 probed means that container was not confirmed running."
        />
        <Stat label="Workers ready" value={m ? String(m.nodes_ready) : "—"} note="Nodes currently sending heartbeats." />
        <Stat label="Workers stale" value={m ? String(m.nodes_dead) : "—"} note="Nodes the platform marked dead after missed heartbeats. Deployments and volumes are not deleted." />
      </div>

      <section className="card" style={{ marginTop: "1.1rem" }}>
        <h2>Worker nodes</h2>
        <p className="hint">
          A ready node can run deployments. A stale node has stopped reporting. Containers stay where they were started; move work by stopping an application and deploying it again.
        </p>
        {nodes.length === 0 ? (
          <p className="empty">No workers have reported yet.</p>
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
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      <details className="card" style={{ marginTop: "0.85rem" }}>
        <summary>Register another worker</summary>
        <form onSubmit={(e) => void onCreate(e)}>
        <div className="form-grid">
          <div>
            <label htmlFor="node-name">Name</label>
            <input
              id="node-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="lab2"
              required
            />
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
            Join token for <strong>{shownName}</strong>, shown once. Put it in that worker&apos;s environment as <code>FORGE_NODE_TOKEN</code> and set <code>FORGE_NODE_NAME</code> to the name above. Do not commit it or paste it into a deployment.
            <br />
            <code className="token">{shownToken}</code>
          </p>
        ) : null}
        </form>
      </details>
    </>
  );
}

function nodeStatusLabel(status: string): string {
  if (status === "ready") {
    return "Ready";
  }
  if (status === "dead") {
    return "Stale";
  }
  return status;
}

function Stat({ label, value, note }: { label: string; value: string; note?: string }) {
  return (
    <div className="stat">
      <span className="stat-label">{label}</span>
      <span className="stat-value">{value}</span>
      {note ? <span className="stat-note">{note}</span> : null}
    </div>
  );
}
