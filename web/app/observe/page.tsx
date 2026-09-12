"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { getMetrics, type ObserveMetrics } from "../api";
import { formatDuration } from "../status";

export default function ObservePage() {
  const [m, setM] = useState<ObserveMetrics | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let alive = true;
    async function load() {
      try {
        const next = await getMetrics();
        if (alive) {
          setM(next);
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
          totals and whether live containers are still running. Streaming app
          logs live on each application&apos;s Logs tab. Tracing and alerts are
          not in this phase.
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
      </div>

      <p className="hint">
        HTTP counters reset when the API process restarts. Deploy history is
        PostgreSQL. Open an application to stream runtime logs.
      </p>
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
