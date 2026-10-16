"use client";

import { FormEvent, useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import {
  cancelProjectDeploy,
  createApplication,
  deployProject,
  getProject,
  getProjectDeploy,
  isBundledSample,
  listApplications,
  listProjectDeploys,
  projectPlan,
  type PlanService,
  type Application,
  type Project,
  type ProjectDeploy,
} from "../../api";

export default function ProjectPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const [project, setProject] = useState<Project | null>(null);
  const [apps, setApps] = useState<Application[]>([]);
  const [plan, setPlan] = useState<PlanService[] | null>(null);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [repo, setRepo] = useState("");
  const [showAdd, setShowAdd] = useState(false);
  const [busy, setBusy] = useState(false);
  const [rollout, setRollout] = useState<ProjectDeploy | null>(null);
  const [history, setHistory] = useState<ProjectDeploy[]>([]);

  async function refresh() {
    const [p, list] = await Promise.all([getProject(id), listApplications(id)]);
    setProject(p);
    setApps(list);
  }

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        await refresh();
        try {
          const found = await projectPlan(id);
          if (!cancelled) {
            setPlan(found);
          }
        } catch {
          if (!cancelled) {
            setPlan([]);
          }
        }
        try {
          const past = await listProjectDeploys(id);
          if (!cancelled) {
            setHistory(past);
            if (past[0]) {
              setRollout(past[0]);
            }
          }
        } catch {
          if (!cancelled) {
            setHistory([]);
          }
        }
      } catch (e) {
        if (!cancelled) {
          setError(e instanceof Error ? e.message : "failed to load");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id]);

  useEffect(() => {
    if (!rollout || rollout.status !== "deploying") {
      return;
    }
    const timer = setInterval(() => {
      void getProjectDeploy(id, rollout.id)
        .then((next) => {
          setRollout(next);
          setHistory((cur) => cur.map((item) => (item.id === next.id ? next : item)));
        })
        .catch(() => undefined);
    }, 3000);
    return () => clearInterval(timer);
  }, [id, rollout]);

  async function onDeployAll() {
    setBusy(true);
    setError("");
    try {
      const next = await deployProject(id);
      setRollout(next);
      setHistory((cur) => [next, ...cur.filter((item) => item.id !== next.id)]);
      const list = await listApplications(id);
      setApps(list);
    } catch (err) {
      setError(err instanceof Error ? err.message : "deploy failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCancelRollout() {
    if (!rollout) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      const next = await cancelProjectDeploy(id, rollout.id);
      setRollout(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : "cancel failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const app = await createApplication(id, name, repo);
      setApps((cur) => [...cur, app]);
      setName("");
      setShowAdd(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "create failed");
    } finally {
      setBusy(false);
    }
  }

  if (!project && !error) {
    return <p className="empty">Loading project…</p>;
  }

  const sample = project ? isBundledSample(project.repository_url) : false;

  return (
    <>
      <p className="crumb">
        <Link href="/">Projects</Link>
      </p>
      <div className="page-head">
        <div>
          <h1>{project?.name ?? "Project"}</h1>
          <p className="page-head-meta">{project?.repository_url}</p>
        </div>
      </div>

      {sample ? (
        <div className="banner">
          This project defaulted to the bundled hello sample. Open an application to deploy, set
          environment variables, or read logs.
        </div>
      ) : null}

      {error ? <p className="error">{error}</p> : null}

      {rollout ? (
        <div className="card" style={{ marginBottom: "0.85rem" }}>
          <h2>Project deployment</h2>
          <p className={rollout.status === "failed" ? "error" : "hint"} style={{ marginTop: 0 }}>
            {rollout.status === "live"
              ? "All required services are healthy."
              : rollout.status === "failed"
                ? "This deployment did not succeed."
                : "Builds are queued. One worker runs them one at a time."}{" "}
            Status: {rollout.status}
          </p>
          {rollout.attention ? <div className="banner">{rollout.attention}</div> : null}
          {rollout.note ? <p className="hint">{rollout.note}</p> : null}
          {rollout.links.length > 0 ? (
            <p className="hint">
              Private links:{" "}
              {rollout.links.map((l) => `${l.source} → ${l.target} (${l.env_key})`).join("; ")}
            </p>
          ) : null}
          <div className="project-list">
            {rollout.services.map((s) => (
              <div className="project-row" key={`${s.application_id}-${s.name}`}>
                <div>
                  <strong>{s.name}</strong>
                  <span>
                    {s.status}
                    {s.package ? ` · ${s.package}` : s.root && s.root !== "." ? ` · ${s.root}` : ""}
                    {s.port ? ` · port ${s.port}` : ""}
                    {s.internal_url ? ` · ${s.internal_url}` : ""}
                    {s.error ? ` · ${s.error}` : ""}
                  </span>
                </div>
                <Link href={`/applications/${s.application_id}`}>Logs</Link>
              </div>
            ))}
          </div>
          <div className="actions">
            {rollout.status === "deploying" ? (
              <button className="btn-ghost" type="button" disabled={busy} onClick={() => void onCancelRollout()}>
                Cancel
              </button>
            ) : null}
            <button className="btn-ghost" type="button" disabled={busy} onClick={() => void onDeployAll()}>
              Deploy again
            </button>
          </div>
        </div>
      ) : null}

      {history.length > 1 ? (
        <div className="card" style={{ marginBottom: "0.85rem" }}>
          <h2>Deployment history</h2>
          <div className="project-list">
            {history.map((item) => (
              <button
                className="project-row"
                key={item.id}
                type="button"
                onClick={() => setRollout(item)}
              >
                <div>
                  <strong>{item.status}</strong>
                  <span>{new Date(item.created_at).toLocaleString()}</span>
                </div>
              </button>
            ))}
          </div>
        </div>
      ) : null}

      {plan && plan.length > 0 ? (
        <div className="card" style={{ marginBottom: "0.85rem" }}>
          <h2>Detected services</h2>
          <p className="hint" style={{ marginTop: 0 }}>
            Found in the repository. Deploy All queues one build per deployable service, dependencies first.
            A library is listed and skipped.
          </p>
          <div className="actions">
            <button className="btn-primary" type="button" disabled={busy} onClick={() => void onDeployAll()}>
              {busy ? "Queueing…" : "Deploy All"}
            </button>
          </div>
          <div className="project-list">
            {plan.map((s) => (
              <div className="project-row" key={`${s.root}-${s.package ?? ""}-${s.name}`}>
                <div>
                  <strong>{s.name}</strong>
                  <span>
                    {s.deployable ? s.kind || "service" : "not deployed"}
                    {s.package ? ` · ${s.package}` : s.root !== "." ? ` · ${s.root}` : ""}
                    {s.detail ? ` · ${s.detail}` : ""}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </div>
      ) : null}

      <div className="card">
        <h2>Applications</h2>
        {apps.length === 0 ? (
          <p className="empty">None yet. Creating a project usually adds one named app.</p>
        ) : (
          <div className="project-list">
            {apps.map((a) => (
              <Link className="project-row" key={a.id} href={`/applications/${a.id}`}>
                <div>
                  <strong>{a.name}</strong>
                  <span>
                    {a.repository_url}
                    {a.local_host ? ` · ${a.local_host}.localhost:9080` : ""}
                  </span>
                </div>
                <span>Deploy</span>
              </Link>
            ))}
          </div>
        )}
      </div>

      {showAdd ? (
        <form className="card" style={{ marginTop: "0.85rem" }} onSubmit={onCreate}>
          <h2>Another service</h2>
          <p className="hint" style={{ marginTop: 0 }}>
            Use this only when the same repository has a second program, such as a separate API.
            The Git URL can stay the same. Set the service folder later on the application.
          </p>
          <div className="form-grid">
            <div>
              <label htmlFor="app-name">Name</label>
              <input id="app-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="api" required />
            </div>
            <div>
              <label htmlFor="app-repo">Git URL</label>
              <input
                id="app-repo"
                value={repo}
                onChange={(e) => setRepo(e.target.value)}
                placeholder="https://github.com/you/app.git"
                required
              />
            </div>
          </div>
          <div className="actions">
            <button className="btn-primary" type="submit" disabled={busy}>
              {busy ? "Creating…" : "Add service"}
            </button>
            <button className="btn-ghost" type="button" onClick={() => setShowAdd(false)}>
              Cancel
            </button>
          </div>
        </form>
      ) : (
        <div className="actions" style={{ marginTop: "0.85rem" }}>
          <button
            className="btn-ghost"
            type="button"
            onClick={() => {
              if (!repo && project && !isBundledSample(project.repository_url)) {
                setRepo(project.repository_url);
              }
              setShowAdd(true);
            }}
          >
            Add another service
          </button>
        </div>
      )}
    </>
  );
}
