"use client";

import { FormEvent, useEffect, useState } from "react";
import Link from "next/link";
import { createNode, getMetrics, listNodes, type ObserveMetrics, type WorkerNode } from "../api";
import { formatDuration } from "../status";

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
          Control-plane request counts for this API process, plus your deploy
          totals, worker nodes, and whether live containers are still running.
          Streaming app logs live on each application&apos;s Logs tab. Tracing
          and alerts are not in this phase.
        </p>
        {error ? <p className="error">{error}</p> : null}
      </section>

      <div className="stat-grid">
        <Stat label="HTTP requests" value={m ? String(m.http_requests_total) : "—"} />
        <Stat label="HTTP 5xx" value={m ? String(m.http_errors_total) : "—"} />
        <Stat label="Deploys queued (process)" value={m ? String(m.deploys_queued_total) : "—"} />
        <Stat label="Your deployments" value={m ? String(m.deployments_total) : "—"} />
        <Stat label="Live" value={m ? String(m.deployments_live) : "—"} />
        <Stat label="Failed" value={m ? String(m.deployments_failed) : "—"} />
        <Stat
          label="Last deploy duration"
          value={m && m.last_deploy_duration_ms ? formatDuration(m.last_deploy_duration_ms) : "—"}
        />
        <Stat
          label="Containers running"
          value={
            m ? `${m.containers_running} / ${m.containers_checked} checked` : "—"
          }
        />
        <Stat label="Nodes ready" value={m ? String(m.nodes_ready) : "—"} />
        <Stat label="Nodes dead" value={m ? String(m.nodes_dead) : "—"} />
      </div>

      <p className="hint">
        HTTP counters reset when the API process restarts. Deploy history is
        PostgreSQL. A dead node does not require dropping the database. Open an
        application to stream runtime logs.
      </p>

      <section className="card" style={{ marginTop: "1.1rem" }}>
        <h2>Worker nodes</h2>
        <p className="hint">
          The reserved <code>local</code> node is created on API start. A second
          process needs a name and a join token. Workload move is Stop, then
          Deploy — containers are not live-migrated.
        </p>
        {nodes.length === 0 ? (
          <p className="empty">No nodes yet. Restart the API so <code>local</code> is created.</p>
        ) : (
          <div className="project-list">
            {nodes.map((n) => (
              <div className="project-row" key={n.id}>
                <div>
                  <strong>{n.name}</strong>
                  <p className="empty">
                    {n.status}
                    {n.advertise_host ? ` · ${n.advertise_host}` : ""}
                    {` · in flight ${n.in_progress}`}
                    {n.last_seen_at ? ` · seen ${n.last_seen_at}` : " · never seen"}
                  </p>
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      <form className="card" style={{ marginTop: "0.85rem" }} onSubmit={(e) => void onCreate(e)}>
        <h2>Register a node</h2>
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
            Join token for <code>{shownName}</code> (shown once). Set{" "}
            <code>FORGE_NODE_NAME={shownName}</code> and{" "}
            <code>FORGE_NODE_TOKEN</code> on that worker. Do not commit it.
            <br />
            <code className="token">{shownToken}</code>
          </p>
        ) : null}
      </form>
    </>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="stat">
      <span className="stat-label">{label}</span>
      <span className="stat-value">{value}</span>
    </div>
  );
}
