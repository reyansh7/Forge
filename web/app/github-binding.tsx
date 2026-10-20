"use client";

import { FormEvent, useEffect, useState } from "react";
import {
  connectApplicationGitHub,
  disconnectApplicationGitHub,
  getApplicationGitHub,
  getGitHubStatus,
  listGitHubRepos,
  type ApplicationGitHub,
  type DeployEnvironment,
  type GitHubRepo,
} from "./api";
import { pushDeployStatus } from "./github-binding-state";

/** GitHub card for one application.

The save button calls the existing PUT /applications/{id}/github.
OAuth tokens and webhook secrets stay on the API. webhook and warning
are shown as the API sent them.
 */
export function GitHubBinding({ applicationId }: { applicationId: string }) {
  const [oauthConnected, setOauthConnected] = useState(false);
  const [configured, setConfigured] = useState(true);
  const [login, setLogin] = useState("");
  const [repos, setRepos] = useState<GitHubRepo[]>([]);
  const [binding, setBinding] = useState<ApplicationGitHub | null>(null);
  const [repo, setRepo] = useState("");
  const [branch, setBranch] = useState("main");
  const [environment, setEnvironment] = useState<DeployEnvironment>("production");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [status, current] = await Promise.all([
          getGitHubStatus(),
          getApplicationGitHub(applicationId),
        ]);
        if (cancelled) {
          return;
        }
        setConfigured(status.configured);
        setOauthConnected(status.connected);
        setLogin(status.login ?? "");
        setBinding(current);
        if (current.connected) {
          setRepo(current.repo ?? "");
          setBranch(current.branch || "main");
          setEnvironment(current.environment === "preview" ? "preview" : "production");
        }
        if (status.connected) {
          const list = await listGitHubRepos();
          if (!cancelled) {
            setRepos(list);
          }
        }
      } catch (e) {
        if (!cancelled) {
          setError(e instanceof Error ? e.message : "Could not load GitHub settings");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [applicationId]);

  function onPickRepo(fullName: string) {
    setRepo(fullName);
    const found = repos.find((item) => item.full_name === fullName);
    if (found?.default_branch && (!binding?.connected || fullName !== binding.repo)) {
      setBranch(found.default_branch);
    }
  }

  async function onSave(e: FormEvent) {
    e.preventDefault();
    setBusy("save");
    setError("");
    try {
      const next = await connectApplicationGitHub(applicationId, repo.trim(), branch.trim(), environment);
      setBinding(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save the GitHub binding");
    } finally {
      setBusy("");
    }
  }

  async function onDisconnect() {
    setBusy("disconnect");
    setError("");
    try {
      await disconnectApplicationGitHub(applicationId);
      setBinding({ connected: false });
      setRepo("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not disconnect the repository");
    } finally {
      setBusy("");
    }
  }

  const status = pushDeployStatus({
    oauthConnected,
    bindingConnected: Boolean(binding?.connected),
    webhook: binding?.webhook,
    warning: binding?.warning,
  });

  return (
    <section className="card">
      <h2>GitHub</h2>
      <p className="hint" style={{ marginTop: 0 }}>
        This is the push binding for this application. The Git URL in Settings is only what a manual Deploy clones.
      </p>
      <p className={status.enabled ? "hint" : "error"}>{status.label}. {status.detail}</p>
      {binding?.connected ? (
        <p className="empty">
          Connected to <strong>{binding.repo}</strong> branch <strong>{binding.branch}</strong> ({binding.environment}).
          Webhook {binding.webhook ? "registered" : "not registered"}.
        </p>
      ) : (
        <p className="empty">No repository is connected to this application.</p>
      )}
      {!configured ? (
        <p className="hint">GitHub OAuth is not configured on this API.</p>
      ) : null}
      {configured && !oauthConnected ? (
        <p className="hint">
          <a href={`/forge-api/integrations/github/start?return_to=/applications/${applicationId}`}>Link GitHub</a>
          {" "}
          to choose a repository. This does not turn on push-to-deploy by itself.
        </p>
      ) : null}
      {error ? <p className="error">{error}</p> : null}
      {oauthConnected ? (
        <form onSubmit={(e) => void onSave(e)}>
          <div className="form-grid">
            <div>
              <label htmlFor="gh-repo">Repository</label>
              <select id="gh-repo" value={repo} onChange={(e) => onPickRepo(e.target.value)} required>
                <option value="">Choose a repository</option>
                {repo && !repos.some((item) => item.full_name === repo) ? <option value={repo}>{repo}</option> : null}
                {repos.map((item) => (
                  <option key={item.full_name} value={item.full_name}>
                    {item.full_name}
                    {item.private ? " (private)" : ""}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label htmlFor="gh-branch">Branch</label>
              <input id="gh-branch" value={branch} onChange={(e) => setBranch(e.target.value)} required />
            </div>
            <div>
              <label htmlFor="gh-env">Environment</label>
              <select
                id="gh-env"
                value={environment}
                onChange={(e) => setEnvironment(e.target.value === "preview" ? "preview" : "production")}
              >
                <option value="production">production</option>
                <option value="preview">preview</option>
              </select>
            </div>
          </div>
          <p className="hint">Signed in to GitHub as {login ? `@${login}` : "a linked account"}.</p>
          <div className="actions">
            <button className="btn-primary" type="submit" disabled={busy !== ""}>
              {busy === "save" ? "Saving…" : binding?.connected ? "Update binding" : "Save binding"}
            </button>
            {binding?.connected ? (
              <button className="btn-ghost" type="button" disabled={busy !== ""} onClick={() => void onDisconnect()}>
                {busy === "disconnect" ? "Disconnecting…" : "Disconnect"}
              </button>
            ) : null}
          </div>
        </form>
      ) : null}
    </section>
  );
}
