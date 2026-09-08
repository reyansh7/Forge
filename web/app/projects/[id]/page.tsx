"use client";

import { FormEvent, useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import {
  createApplication,
  getProject,
  isBundledSample,
  listApplications,
  type Application,
  type Project,
} from "../../api";

export default function ProjectPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const [project, setProject] = useState<Project | null>(null);
  const [apps, setApps] = useState<Application[]>([]);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [repo, setRepo] = useState("forge://hello");
  const [busy, setBusy] = useState(false);

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

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const app = await createApplication(id, name, repo);
      setApps((cur) => [...cur, app]);
      setName("");
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
                <span>Open</span>
              </Link>
            ))}
          </div>
        )}
      </div>

      <form className="card" style={{ marginTop: "0.85rem" }} onSubmit={onCreate}>
        <h2>New application</h2>
        <div className="form-grid">
          <div>
            <label htmlFor="app-name">Name</label>
            <input id="app-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="api" />
          </div>
          <div>
            <label htmlFor="app-repo">Git URL</label>
            <input
              id="app-repo"
              value={repo}
              onChange={(e) => setRepo(e.target.value)}
              placeholder="forge://hello"
            />
          </div>
        </div>
        <div className="actions">
          <button className="btn-primary" type="submit" disabled={busy}>
            {busy ? "Creating…" : "Add application"}
          </button>
        </div>
      </form>
    </>
  );
}
