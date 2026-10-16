"use client";

import { FormEvent, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  createProject,
  getGitHubStatus,
  listGitHubRepos,
  listProjects,
  type GitHubRepo,
  type Project,
} from "./api";
import { CountUp, SplitHeading } from "./split";

export default function HomePage() {
  const router = useRouter();
  const [projects, setProjects] = useState<Project[]>([]);
  const [name, setName] = useState("");
  const [repo, setRepo] = useState("https://");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<"sample" | "custom" | "">("");
  const [githubLogin, setGithubLogin] = useState("");
  const [repos, setRepos] = useState<GitHubRepo[]>([]);
  const [picked, setPicked] = useState("");

  async function refresh() {
    try {
      setProjects(await listProjects());
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to load projects");
    }
  }

  useEffect(() => {
    void refresh();
    void (async () => {
      try {
        const status = await getGitHubStatus();
        if (!status.connected) {
          return;
        }
        setGithubLogin(status.login ?? "");
        setRepos(await listGitHubRepos());
      } catch (e) {
        setError(e instanceof Error ? e.message : "failed to list GitHub repositories");
      }
    })();
  }, []);

  function onPickRepo(fullName: string) {
    setPicked(fullName);
    const found = repos.find((r) => r.full_name === fullName);
    if (!found) {
      return;
    }
    const short = found.full_name.split("/")[1] || found.full_name;
    setName(short.slice(0, 100));
    setRepo(`https://github.com/${found.full_name}.git`);
  }

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
    if (!name.trim()) {
      setError("Type a project name, or choose one of your GitHub repositories. The grey text in the box is only a hint.");
      return;
    }
    if (!repo.trim() || repo.trim() === "https://") {
      setError("Choose a repository or paste a full https:// git URL.");
      return;
    }
    await makeProject(name, repo, "custom");
  }

  return (
    <>
      <section className="hero">
        <span className="eyebrow">Control plane</span>
        <SplitHeading lines={[{ text: "Deploy from git," }, { text: "on your own metal.", gradient: true }]} />
        <p className="lede">
          Create a project, ship an application, and open it on a localhost URL. Start with the
          bundled sample if you do not have a repository yet.
        </p>
      </section>

      <div className="metric-row">
        <div className="stat">
          <span className="stat-label">Projects</span>
          <div className="metric-value">
            <CountUp value={projects.length} />
          </div>
        </div>
        <div className="stat">
          <span className="stat-label">GitHub</span>
          <div className="metric-value small">{githubLogin ? `@${githubLogin}` : "Not linked"}</div>
        </div>
        <div className="stat">
          <span className="stat-label">Engine</span>
          <div className="metric-value small">Docker · Caddy · Postgres</div>
        </div>
      </div>

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
          {githubLogin ? (
            <>
              <label htmlFor="gh-pick">Your GitHub repositories</label>
              <select id="gh-pick" value={picked} onChange={(e) => onPickRepo(e.target.value)}>
                <option value="">Choose a repository</option>
                {repos.map((r) => (
                  <option key={r.full_name} value={r.full_name}>
                    {r.full_name}
                    {r.private ? " (private)" : ""}
                  </option>
                ))}
              </select>
              <p className="hint">
                Signed in to GitHub as <strong>{githubLogin}</strong>. Choosing a repository fills the
                name and URL. Private clones use that GitHub account.
              </p>
            </>
          ) : null}
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
          <p className="empty">
            {githubLogin
              ? "No projects on this GitHub sign-in yet. Projects from a password login stay on that account. Choose a repository above to create one here."
              : "None yet. Deploy the sample to see the full loop."}
          </p>
        ) : (
          <div className="project-list">
            {projects.map((p) => (
              <Link className="project-row" key={p.id} href={`/projects/${p.id}`}>
                <div className="project-main">
                  <div className="avatar" aria-hidden="true">
                    {p.name.slice(0, 1).toUpperCase()}
                  </div>
                  <div>
                    <strong>{p.name}</strong>
                    <span>{p.repository_url}</span>
                  </div>
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
