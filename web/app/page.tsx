"use client";

import { FormEvent, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { createProject, listProjects, type Project } from "./api";

export default function HomePage() {
  const router = useRouter();
  const [projects, setProjects] = useState<Project[]>([]);
  const [name, setName] = useState("");
  const [repo, setRepo] = useState("https://");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<"sample" | "custom" | "">("");

  async function refresh() {
    try {
      setProjects(await listProjects());
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to load projects");
    }
  }

  useEffect(() => {
    void refresh();
  }, []);

  async function makeProject(projectName: string, repositoryURL: string, mode: "sample" | "custom") {
    setBusy(mode);
    setError("");
    try {
      const p = await createProject(projectName, repositoryURL);
      router.push(`/projects/${p.id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "create failed");
    } finally {
      setBusy("");
    }
  }

  async function onSample() {
    await makeProject("hello-sample", "forge://hello", "sample");
  }

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    await makeProject(name, repo, "custom");
  }

  return (
    <>
      <section className="hero">
        <h1>Deploy locally from git.</h1>
        <p className="lede">
          Create a project, ship an application, and open it on a localhost URL. Start with the
          bundled sample if you do not have a repository yet.
        </p>
      </section>

      <div className="grid">
        <div className="card">
          <h2>Quick start</h2>
          <p className="lede" style={{ marginBottom: "0.35rem" }}>
            Deploys <code>forge://hello</code>. No real git remote required.
          </p>
          <div className="actions">
            <button className="btn-primary" type="button" onClick={onSample} disabled={!!busy}>
              {busy === "sample" ? "Creating…" : "Deploy sample app"}
            </button>
          </div>
          <p className="hint">
            Placeholder URLs like <code>https://github.com/example/forge.git</code> also use this
            sample. A real deploy needs a public <code>https://</code> repository that exists.
          </p>
        </div>

        <form className="card" onSubmit={onCreate}>
          <h2>New project</h2>
          <label htmlFor="name">Project name</label>
          <input
            id="name"
            placeholder="storefront"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <label htmlFor="repo">Git URL</label>
          <input
            id="repo"
            placeholder="https://github.com/you/app.git"
            value={repo}
            onChange={(e) => setRepo(e.target.value)}
          />
          <div className="actions">
            <button className="btn-ghost" type="submit" disabled={!!busy}>
              {busy === "custom" ? "Creating…" : "Create project"}
            </button>
          </div>
        </form>
      </div>

      {error ? <p className="error">{error}</p> : null}

      <div className="card" style={{ marginTop: "0.85rem" }}>
        <h2>Projects</h2>
        {projects.length === 0 ? (
          <p className="empty">None yet. Deploy the sample to see the full loop.</p>
        ) : (
          <div className="project-list">
            {projects.map((p) => (
              <Link className="project-row" key={p.id} href={`/projects/${p.id}`}>
                <div>
                  <strong>{p.name}</strong>
                  <span>{p.repository_url}</span>
                </div>
                <span>Open</span>
              </Link>
            ))}
          </div>
        )}
      </div>
    </>
  );
}
