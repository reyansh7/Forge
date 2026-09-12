"use client";

/**
 * Application dashboard. Talks only to the Go API via /forge-api.
 * Status/health poll; runtime logs use an authorized SSE follow.
 * Env hide is UX only — the API still returns plaintext.
 */
import { FormEvent, useEffect, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import {
  createApplicationDeployment,
  deleteApplication,
  deleteEnv,
  getApplication,
  getApplicationHealth,
  getApplicationLogs,
  getDeployment,
  isBundledSample,
  listApplicationDeployments,
  listEnv,
  mergeEnv,
  parseDotEnvDetailed,
  putEnv,
  replaceEnv,
  rollbackDeployment,
  stopApplication,
  updateApplication,
  type AppHealth,
  type Application,
  type Deployment,
  type EnvVar,
} from "../../api";
import { formatDuration, isInProgress, pillClass, statusLabel, STAGES, stepClass, when } from "../../status";

const TABS = [
  { id: "overview", label: "Overview" },
  { id: "deployments", label: "Deployments" },
  { id: "logs", label: "Logs" },
  { id: "environment", label: "Environment" },
  { id: "settings", label: "Settings" },
] as const;

type Tab = (typeof TABS)[number]["id"];

export default function ApplicationPage() {
  const params = useParams<{ id: string }>();
  const router = useRouter();
  const id = params.id;
  const [tab, setTab] = useState<Tab>("overview");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [app, setApp] = useState<Application | null>(null);
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [env, setEnv] = useState<EnvVar[]>([]);
  const [health, setHealth] = useState<AppHealth | null>(null);
  const [logs, setLogs] = useState("");
  const [streamStatus, setStreamStatus] = useState<"off" | "live" | "error">("off");
  const [name, setName] = useState("");
  const [repo, setRepo] = useState("");
  const [rootDirectory, setRootDirectory] = useState(".");
  const [healthPath, setHealthPath] = useState("/");
  const [localHost, setLocalHost] = useState("");
  const [envKey, setEnvKey] = useState("");
  const [envValue, setEnvValue] = useState("");
  const [envBulk, setEnvBulk] = useState("");
  const [envFileName, setEnvFileName] = useState("");
  const [showEnv, setShowEnv] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");

  async function refresh() {
    const [a, list, vars] = await Promise.all([
      getApplication(id),
      listApplicationDeployments(id),
      listEnv(id),
    ]);
    setApp(a);
    setName(a.name);
    setRepo(a.repository_url);
    setRootDirectory(a.root_directory || ".");
    setHealthPath(a.health_path || "/");
    setLocalHost(a.local_host || "");
    setDeployments(list);
    setEnv(vars);
    try {
      setHealth(await getApplicationHealth(id));
    } catch {
      setHealth(null);
    }
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

  useEffect(() => {
    const pending = deployments.find((d) => isInProgress(d.status));
    const live = deployments.find((d) => d.status === "live");
    if (!pending && !live) {
      return;
    }
    const t = window.setInterval(() => {
      void (async () => {
        if (pending) {
          try {
            const d = await getDeployment(pending.id);
            setDeployments((cur) => cur.map((x) => (x.id === d.id ? d : x)));
          } catch {
            /* ignore transient poll errors */
          }
        }
        try {
          setHealth(await getApplicationHealth(id));
        } catch {
          /* health may be stopped */
        }
      })();
    }, 1500);
    return () => window.clearInterval(t);
  }, [deployments, id]);

  const liveId = deployments.find((d) => d.status === "live")?.id ?? "";

  useEffect(() => {
    if (tab !== "logs" || !liveId) {
      setStreamStatus("off");
      return;
    }
    const es = new EventSource(`/forge-api/applications/${id}/logs/stream?tail=200`);
    setStreamStatus("live");
    es.onmessage = (ev) => {
      try {
        const data = JSON.parse(ev.data) as { line?: string; error?: string };
        if (data.error) {
          setStreamStatus("error");
          return;
        }
        const line = data.line;
        if (line == null) {
          return;
        }
        setLogs((cur) => {
          const next = cur && cur !== "(empty)" ? `${cur}\n${line}` : line;
          const lines = next.split("\n");
          return lines.length > 2000 ? lines.slice(-2000).join("\n") : next;
        });
      } catch {
        /* ignore a malformed event */
      }
    };
    es.onerror = () => {
      setStreamStatus("error");
      es.close();
    };
    return () => {
      es.close();
    };
  }, [tab, id, liveId]);

  async function onSave(e: FormEvent) {
    e.preventDefault();
    setBusy("save");
    setError("");
    try {
      const next = await updateApplication(id, {
        name,
        repository_url: repo,
        root_directory: rootDirectory,
        health_path: healthPath,
        local_host: localHost,
      });
      setApp(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : "save failed");
    } finally {
      setBusy("");
    }
  }

  async function onDeploy() {
    setBusy("deploy");
    setError("");
    try {
      const d = await createApplicationDeployment(id);
      setDeployments((cur) => [d, ...cur]);
      setSelectedId(d.id);
      setTab("deployments");
    } catch (err) {
      setError(err instanceof Error ? err.message : "deploy failed");
    } finally {
      setBusy("");
    }
  }

  async function onStop() {
    setBusy("stop");
    setError("");
    try {
      const d = await stopApplication(id);
      setDeployments((cur) => {
        const next = cur.map((x) => (x.id === d.id ? d : x));
        return next.some((x) => x.id === d.id) ? next : [d, ...next];
      });
      try {
        setHealth(await getApplicationHealth(id));
      } catch {
        setHealth(null);
      }
      setDeployments(await listApplicationDeployments(id));
    } catch (err) {
      setError(err instanceof Error ? err.message : "stop failed");
    } finally {
      setBusy("");
    }
  }

  async function onLogs() {
    setBusy("logs");
    setError("");
    try {
      const got = await getApplicationLogs(id, 200);
      setLogs(got.text || "(empty)");
    } catch (err) {
      setError(err instanceof Error ? err.message : "logs failed");
    } finally {
      setBusy("");
    }
  }

  async function onEnv(e: FormEvent) {
    e.preventDefault();
    setBusy("env");
    setError("");
    try {
      await putEnv(id, envKey, envValue);
      setEnv(await listEnv(id));
      setEnvKey("");
      setEnvValue("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "env failed");
    } finally {
      setBusy("");
    }
  }

  const envParsed = parseDotEnvDetailed(envBulk);

  async function applyEnvFile(mode: "merge" | "replace") {
    setBusy("envbulk");
    setError("");
    try {
      if (envParsed.vars.length === 0) {
        throw new Error("no KEY=VALUE lines found in the pasted or uploaded file");
      }
      const vars = mode === "merge" ? mergeEnv(env, envParsed.vars) : envParsed.vars;
      setEnv(await replaceEnv(id, vars));
      setEnvBulk("");
      setEnvFileName("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "env import failed");
    } finally {
      setBusy("");
    }
  }

  async function onEnvFile(file: File | undefined) {
    if (!file) {
      return;
    }
    if (file.size > 256 * 1024) {
      setError("env file must be at most 256 KB");
      return;
    }
    setError("");
    const text = await file.text();
    setEnvBulk(text);
    setEnvFileName(file.name);
  }

  async function onDeleteEnv(key: string) {
    setError("");
    try {
      await deleteEnv(id, key);
      setEnv(await listEnv(id));
    } catch (err) {
      setError(err instanceof Error ? err.message : "delete env failed");
    }
  }

  async function onRollback(sourceId: string) {
    setBusy("rollback");
    setError("");
    try {
      const d = await rollbackDeployment(sourceId);
      setDeployments((cur) => [d, ...cur]);
      setSelectedId(d.id);
      setTab("deployments");
    } catch (err) {
      setError(err instanceof Error ? err.message : "rollback failed");
    } finally {
      setBusy("");
    }
  }

  async function onDelete() {
    if (!window.confirm("Delete this application and its deployments?")) {
      return;
    }
    setBusy("delete");
    setError("");
    try {
      await deleteApplication(id);
      if (app) {
        router.push(`/projects/${app.project_id}`);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "delete failed");
      setBusy("");
    }
  }

  if (!app && !error) {
    return <p className="empty">Loading application…</p>;
  }

  const sample = app ? isBundledSample(app.repository_url) : false;
  const inProgress = deployments.some((d) => isInProgress(d.status));
  const liveRow = deployments.find((d) => d.status === "live");
  const latest = deployments[0];
  const selected =
    deployments.find((d) => d.id === selectedId) ?? (tab === "deployments" ? deployments[0] : null) ?? null;
  const running = health?.status === "healthy";
  const deployBlocked = inProgress || (!!liveRow && running);
  const localURL = app?.local_host ? `http://${app.local_host}.localhost:9080/` : "";
  const publicURL = health?.public_url || liveRow?.public_url || "";
  const pathURL = liveRow ? `http://127.0.0.1:9080/d/${liveRow.id}/` : "";
  const visitURL = localURL || publicURL || pathURL;
  const visitLabel = localURL
    ? `${app?.local_host}.localhost:9080`
    : publicURL.includes(".localhost:")
      ? publicURL.replace(/^https?:\/\//, "").replace(/\/$/, "")
      : publicURL;
  const pending = deployments.find((d) => isInProgress(d.status));
  const healthLabel = health?.status ?? (liveRow ? "unknown" : "stopped");
  const productionLabel = liveRow ? (running ? "Live" : healthLabel) : "Offline";
  const deployStatus = pending
    ? statusLabel(pending.status)
    : latest
      ? statusLabel(latest.status)
      : "No deployments";

  return (
    <>
      <p className="crumb">
        <Link href="/">Projects</Link>
        {app ? (
          <>
            {" / "}
            <Link href={`/projects/${app.project_id}`}>Project</Link>
          </>
        ) : null}
      </p>
      <div className="page-head">
        <div>
          <h1>{app?.name ?? "Application"}</h1>
          <p className="page-head-meta">
            {liveRow && running ? (
              <span className="pill live">Live</span>
            ) : pending ? (
              <span className={pillClass(pending.status)}>{statusLabel(pending.status)}</span>
            ) : liveRow ? (
              <span className="pill pending">{statusLabel(health?.status ?? "unknown")}</span>
            ) : latest ? (
              <span className={pillClass(latest.status)}>{statusLabel(latest.status)}</span>
            ) : (
              <span className="pill stopped">Offline</span>
            )}
            <span>{app?.repository_url}</span>
          </p>
        </div>
        <div className="actions-row">
          <button className="btn-primary" type="button" onClick={onDeploy} disabled={!!busy || deployBlocked}>
            {busy === "deploy" ? "Queueing…" : liveRow && running ? "Already live" : "Deploy"}
          </button>
          <button className="btn-ghost" type="button" onClick={onStop} disabled={!!busy}>
            {busy === "stop" ? "Stopping…" : "Stop"}
          </button>
        </div>
      </div>

      {sample ? (
        <div className="banner">
          {liveRow && running
            ? "This app is running. Stop it before deploying or rolling back. Environment and settings apply on the next deploy."
            : "github.com/example and forge://hello use the bundled sample (no GitHub clone). Keep the worker running, then Deploy."}
        </div>
      ) : liveRow && running ? (
        <div className="banner">This app is running. Stop it before deploying or rolling back.</div>
      ) : null}

      {error ? <p className="error">{error}</p> : null}

      <div className="tabs" role="tablist" aria-label="Application">
        {TABS.map((item) => (
          <button
            key={item.id}
            className="tab"
            type="button"
            role="tab"
            aria-selected={tab === item.id}
            onClick={() => setTab(item.id)}
          >
            {item.label}
          </button>
        ))}
      </div>

      {tab === "overview" ? (
        <div className="stack">
          <div className="stat-grid">
            <div className="stat">
              <span className="stat-label">Production</span>
              <div className="stat-value">{productionLabel}</div>
            </div>
            <div className="stat">
              <span className="stat-label">Deployment</span>
              <div className="stat-value">{deployStatus}</div>
            </div>
            <div className="stat">
              <span className="stat-label">Health</span>
              <div className="stat-value">{healthLabel}</div>
            </div>
            <div className="stat">
              <span className="stat-label">URL</span>
              <div className="stat-value url-list">
                {visitURL ? (
                  <a href={visitURL} target="_blank" rel="noreferrer">
                    {visitLabel}
                  </a>
                ) : (
                  <span>Not published</span>
                )}
              </div>
            </div>
          </div>

          <div className="card">
            <h2>Latest deployment</h2>
            {latest ? (
              <>
                <div className="deploy-top">
                  <span className={pillClass(latest.status)}>{statusLabel(latest.status)}</span>
                  <span className="deploy-meta" style={{ margin: 0 }}>
                    {when(latest.created_at)}
                    {latest.rollback_of ? " · Rollback" : ""}
                  </span>
                </div>
                <div className="pipeline" aria-label="deployment stages">
                  {STAGES.map((stage) => (
                    <span key={stage} className={stepClass(stage, latest)}>
                      {statusLabel(stage)}
                    </span>
                  ))}
                </div>
                {latest.error_message ? <div className="err-box">{latest.error_message}</div> : null}
                <div className="actions-row">
                  <button
                    className="btn-ghost"
                    type="button"
                    onClick={() => {
                      setSelectedId(latest.id);
                      setTab("deployments");
                    }}
                  >
                    View details
                  </button>
                  {latest.status === "live" && (visitURL || latest.public_url) ? (
                    <a className="btn btn-ghost" href={visitURL || latest.public_url} target="_blank" rel="noreferrer">
                      Open app
                    </a>
                  ) : null}
                </div>
              </>
            ) : (
              <p className="empty">No deployments yet. Deploy to start the first build.</p>
            )}
          </div>

          <div className="card">
            <h2>Recent deployments</h2>
            {deployments.length === 0 ? (
              <p className="empty">None yet.</p>
            ) : (
              deployments.slice(0, 5).map((d) => (
                <button
                  key={d.id}
                  className="deploy-row"
                  type="button"
                  onClick={() => {
                    setSelectedId(d.id);
                    setTab("deployments");
                  }}
                >
                  <span className={pillClass(d.status)}>{statusLabel(d.status)}</span>
                  <span className="deploy-row-time">
                    {when(d.created_at)}
                    {d.rollback_of ? " · Rollback" : ""}
                  </span>
                  <span className="muted-link">Details</span>
                </button>
              ))
            )}
          </div>
        </div>
      ) : null}

      {tab === "deployments" ? (
        deployments.length === 0 ? (
          <div className="card">
            <h2>Deployments</h2>
            <p className="empty">None yet. Deploy queues work for the worker process.</p>
          </div>
        ) : (
          <div className="deploy-split">
            <div className="deploy-list">
              {deployments.map((d) => (
                <button
                  key={d.id}
                  className="deploy-row"
                  type="button"
                  aria-current={selected?.id === d.id}
                  onClick={() => setSelectedId(d.id)}
                >
                  <span className={pillClass(d.status)}>{statusLabel(d.status)}</span>
                  <span className="deploy-row-time">
                    {when(d.created_at)}
                    {d.rollback_of ? " · Rollback" : ""}
                  </span>
                </button>
              ))}
            </div>
            <div className="deploy-detail">
              {selected ? (
                <DeploymentDetail
                  d={selected}
                  busy={busy}
                  canRollback={!!selected.image_name && !inProgress && !(!!liveRow && running)}
                  onRollback={onRollback}
                />
              ) : (
                <p className="empty">Select a deployment to see build status and logs.</p>
              )}
            </div>
          </div>
        )
      ) : null}

      {tab === "logs" ? (
        <div className="card">
          <div className="deploy-top">
            <h2 style={{ margin: 0 }}>Runtime logs</h2>
            <button className="btn-ghost" type="button" onClick={onLogs} disabled={!!busy}>
              {busy === "logs" ? "Reading…" : "Refresh logs"}
            </button>
          </div>
          <p className="hint" style={{ marginTop: "0.45rem", marginBottom: "0.75rem" }}>
            {streamStatus === "live"
              ? "Live stream of the running container. Refresh still takes a snapshot."
              : streamStatus === "error"
                ? "Stream ended. Use Refresh for a snapshot, or reopen this tab after the app is live."
                : "Runtime logs appear after a successful deploy. A live app streams automatically."}
          </p>
          {logs ? <pre className="log-box">{logs}</pre> : <p className="empty">No logs yet.</p>}
        </div>
      ) : null}

      {tab === "environment" ? (
        <div className="card">
          <div className="deploy-top">
            <h2 style={{ margin: 0 }}>Environment</h2>
            <button className="btn-ghost" type="button" onClick={() => setShowEnv((v) => !v)}>
              {showEnv ? "Hide values" : "Show values"}
            </button>
          </div>
          <p className="hint" style={{ marginTop: "0.45rem" }}>
            Applied on the next deploy. Vite, Next.js, and CRA public keys (VITE_*,
            NEXT_PUBLIC_*, REACT_APP_*) are baked in at build time — changing them
            requires a new deploy, not just a running container. PORT and reserved
            Forge keys are rejected.
          </p>
          {env.length === 0 ? (
            <p className="empty" style={{ marginTop: "0.85rem" }}>
              No variables yet.
            </p>
          ) : (
            <table className="env-table" style={{ marginTop: "0.75rem" }}>
              <thead>
                <tr>
                  <th>Key</th>
                  <th>Value</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {env.map((ev) => (
                  <tr key={ev.key}>
                    <td>
                      <code>{ev.key}</code>
                    </td>
                    <td className="env-value">{showEnv ? ev.value : "••••••••"}</td>
                    <td>
                      <button className="btn-ghost" type="button" onClick={() => onDeleteEnv(ev.key)}>
                        Remove
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <form onSubmit={onEnv} style={{ marginTop: "1rem" }}>
            <div className="env-row">
              <input
                placeholder="KEY"
                value={envKey}
                onChange={(e) => setEnvKey(e.target.value)}
                aria-label="env key"
              />
              <input
                placeholder="value"
                value={envValue}
                onChange={(e) => setEnvValue(e.target.value)}
                aria-label="env value"
              />
              <button className="btn-primary" type="submit" disabled={!!busy}>
                Save
              </button>
            </div>
          </form>
          <form
            className="env-import"
            onSubmit={(e) => {
              e.preventDefault();
              void applyEnvFile("merge");
            }}
          >
            <label htmlFor="env-bulk">Import a .env file</label>
            <p className="hint" style={{ marginTop: "0.35rem" }}>
              Upload a file or paste the whole contents. Comments, quotes, and
              <code> export KEY=value</code> lines are accepted. Import keeps existing
              keys and overwrites matches. Replace all drops keys that are not in the file.
            </p>
            <div className="actions-row" style={{ margin: "0.65rem 0" }}>
              <label className="btn btn-ghost env-file-btn">
                {envFileName ? envFileName : "Choose .env file"}
                <input
                  type="file"
                  accept=".env,.env.*,text/plain"
                  onChange={(e) => {
                    void onEnvFile(e.target.files?.[0]);
                    e.target.value = "";
                  }}
                />
              </label>
            </div>
            <textarea
              id="env-bulk"
              value={envBulk}
              onChange={(e) => {
                setEnvBulk(e.target.value);
                if (envFileName) {
                  setEnvFileName("");
                }
              }}
              placeholder={'VITE_APP_EMAILJS_PUBLIC_KEY=...\nDATABASE_URL="postgres://..."\n# comments are ignored'}
              aria-label="dotenv file contents"
            />
            {envBulk.trim() ? (
              <p className="env-preview">
                {envParsed.vars.length === 0
                  ? "No variables detected yet."
                  : `Detected ${envParsed.vars.length} variable${envParsed.vars.length === 1 ? "" : "s"}: ${envParsed.vars.map((v) => v.key).join(", ")}`}
                {envParsed.skipped.length > 0 ? (
                  <>
                    <br />
                    Skipped: {envParsed.skipped.slice(0, 6).join("; ")}
                    {envParsed.skipped.length > 6 ? "…" : ""}
                  </>
                ) : null}
              </p>
            ) : null}
            <div className="actions-row" style={{ marginTop: "0.65rem" }}>
              <button className="btn-primary" type="submit" disabled={!!busy || envParsed.vars.length === 0}>
                {busy === "envbulk" ? "Saving…" : "Import variables"}
              </button>
              <button
                className="btn-ghost"
                type="button"
                disabled={!!busy || envParsed.vars.length === 0}
                onClick={() => void applyEnvFile("replace")}
              >
                Replace all
              </button>
            </div>
          </form>
        </div>
      ) : null}

      {tab === "settings" ? (
        <form className="card" onSubmit={onSave}>
          <h2>Settings</h2>
          <p className="hint" style={{ marginTop: 0 }}>
            These change the next deploy, not the currently running app. A local host slug is
            optional — unnamed apps still deploy. Forge assigns a Host URL from the application
            name when the slug is empty. Health path should be a route that returns 2xx (APIs
            that 404 on / need something like /api/customers).
          </p>
          <div className="form-grid">
            <div>
              <label htmlFor="name">Name</label>
              <input id="name" value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <div>
              <label htmlFor="repo">Git URL</label>
              <input id="repo" value={repo} onChange={(e) => setRepo(e.target.value)} />
            </div>
            <div>
              <label htmlFor="root">Root directory</label>
              <input
                id="root"
                value={rootDirectory}
                onChange={(e) => setRootDirectory(e.target.value)}
                placeholder="."
              />
            </div>
            <div>
              <label htmlFor="health-path">Health path</label>
              <input
                id="health-path"
                value={healthPath}
                onChange={(e) => setHealthPath(e.target.value)}
                placeholder="/"
              />
            </div>
            <div>
              <label htmlFor="local-host">Local host slug</label>
              <input
                id="local-host"
                value={localHost}
                onChange={(e) => setLocalHost(e.target.value)}
                placeholder="myapp"
              />
            </div>
          </div>
          <div className="actions-row" style={{ marginTop: "1rem" }}>
            <button className="btn-primary" type="submit" disabled={!!busy}>
              {busy === "save" ? "Saving…" : "Save settings"}
            </button>
          </div>
          <div className="danger-zone">
            <h2>Danger zone</h2>
            <p className="hint" style={{ marginTop: 0, marginBottom: "0.75rem" }}>
              Deletes this application and its deployment history.
            </p>
            <button className="btn-danger" type="button" onClick={onDelete} disabled={!!busy}>
              Delete application
            </button>
          </div>
        </form>
      ) : null}
    </>
  );
}

function DeploymentDetail({
  d,
  busy,
  canRollback,
  onRollback,
}: {
  d: Deployment;
  busy: string;
  canRollback: boolean;
  onRollback: (id: string) => void;
}) {
  return (
    <>
      <div className="deploy-top">
        <span className={pillClass(d.status)}>{statusLabel(d.status)}</span>
        <div className="actions-row">
          {d.status === "live" && d.public_url ? (
            <a className="btn btn-ghost" href={d.public_url} target="_blank" rel="noreferrer">
              Open app
            </a>
          ) : null}
          {canRollback ? (
            <button
              className="btn-primary"
              type="button"
              disabled={!!busy}
              onClick={() => onRollback(d.id)}
            >
              {busy === "rollback" ? "Queueing…" : "Rollback to this image"}
            </button>
          ) : null}
        </div>
      </div>
      <p className="deploy-meta">
        {when(d.created_at)}
        {d.duration_ms ? ` · ${formatDuration(d.duration_ms)}` : ""}
        {d.failed_stage ? ` · failed at ${d.failed_stage}` : ""}
        {d.rollback_of ? " · Rollback" : ""}
      </p>
      <div className="pipeline" aria-label="deployment stages">
        {STAGES.map((stage) => (
          <span key={stage} className={stepClass(stage, d)}>
            {statusLabel(stage)}
          </span>
        ))}
      </div>
      {d.error_message ? <div className="err-box">{d.error_message}</div> : null}
      <dl className="runtime-meta">
        <div>
          <dt>Container</dt>
          <dd>{d.container_name || "—"}</dd>
        </div>
        <div>
          <dt>Image</dt>
          <dd>{d.image_name || "—"}</dd>
        </div>
        <div>
          <dt>Runtime</dt>
          <dd>{d.runtime_type || d.runtime_kind || "—"}</dd>
        </div>
        <div>
          <dt>Application port</dt>
          <dd>{d.listen_port || "—"}</dd>
        </div>
        <div>
          <dt>Published port</dt>
          <dd>{d.host_port || "—"}</dd>
        </div>
        <div>
          <dt>Port source</dt>
          <dd>{d.port_source || "—"}</dd>
        </div>
      </dl>
      {d.build_log ? (
        <>
          <h2 className="section-gap">Build log</h2>
          <pre className="log-box">{d.build_log}</pre>
        </>
      ) : (
        <p className="empty">No build log for this deployment.</p>
      )}
    </>
  );
}
