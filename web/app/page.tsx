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

function isSampleURL(value: string): boolean {
  const url = value.trim().toLowerCase();
  return url === "forge://hello" || url.includes("github.com/example/");
}

function validateRepositoryURL(value: string): string {
  const url = value.trim();
  if (!url || url === "https://") {
    return "Choose a repository or paste a full https:// Git URL.";
  }
  if (url === "forge://hello") {
    return "";
  }
  if (!url.startsWith("https://")) {
    return "Repository URLs must start with https://. SSH remotes are not accepted.";
  }
  try {
    const parsed = new URL(url);
    if (!parsed.hostname || !parsed.hostname.includes(".")) {
      return "That URL does not include a host name.";
    }
  } catch {
    return "That is not a valid URL.";
  }
  return "";
}

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
  const [repoQuery, setRepoQuery] = useState("");
  const [reposLoading, setReposLoading] = useState(false);

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
        setReposLoading(true);
        setRepos(await listGitHubRepos());
      } catch (e) {
        setError(e instanceof Error ? e.message : "Could not load GitHub repositories. You can still paste an https Git URL.");
      } finally {
        setReposLoading(false);
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
    const projectName = name.trim();
    const repositoryURL = repo.trim();
    if (!projectName) {
      setError("Enter a project name, or choose one of your GitHub repositories.");
      return;
    }
    if (projectName.length > 100) {
      setError("Project names must be 100 characters or fewer.");
      return;
    }
    const urlError = validateRepositoryURL(repositoryURL);
    if (urlError) {
      setError(urlError);
      return;
    }
    await makeProject(projectName, repositoryURL, "custom");
  }

  const visibleRepos = repos.filter((r) => r.full_name.toLowerCase().includes(repoQuery.trim().toLowerCase()));
  const sampleURL = isSampleURL(repo);

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
            Creates a project that runs the bundled <code>forge://hello</code> app. It is not a GitHub repository and cannot receive pushes.
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
              <label htmlFor="gh-search">Search repositories</label>
              <input
                id="gh-search"
                value={repoQuery}
                onChange={(e) => setRepoQuery(e.target.value)}
                placeholder={reposLoading ? "Loading repositories…" : "Filter by name"}
                disabled={reposLoading}
              />
              <label htmlFor="gh-pick">Your GitHub repositories</label>
              <select id="gh-pick" value={picked} onChange={(e) => onPickRepo(e.target.value)} disabled={reposLoading}>
                <option value="">{reposLoading ? "Loading…" : visibleRepos.length === 0 ? "No matching repositories" : "Choose a repository"}</option>
                {visibleRepos.map((r) => (
                  <option key={r.full_name} value={r.full_name}>
                    {r.full_name}
                    {r.private ? " (private)" : " (public)"}
                  </option>
                ))}
              </select>
              <p className="hint">
                Signed in to GitHub as <strong>{githubLogin}</strong>. Choosing a repository fills the
                name and URL. Public repositories clone without extra credentials. Private repositories
                clone with this GitHub account.
              </p>
            </>
          ) : (
            <p className="hint">GitHub is not linked. Paste an https Git URL, or use the sample app. Private repositories need a linked GitHub account.</p>
          )}
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
          {sampleURL ? (
            <p className="hint">This URL is the bundled sample, not a repository on GitHub. Use the sample button if that is what you want.</p>
          ) : null}
          <div className="actions">
            <button className="btn-primary" type="submit" disabled={!!busy}>
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
