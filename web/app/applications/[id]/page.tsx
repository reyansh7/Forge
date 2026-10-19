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
  createDomain,
  createLink,
  createVolume,
  deleteApplication,
  deleteDomain,
  deleteEnv,
  deleteLink,
  deleteVolume,
  getApplication,
  getApplicationHealth,
  getApplicationLogs,
  getDeployment,
  isBundledSample,
  listApplicationDeployments,
  listApplications,
  listDomains,
  listEnv,
  listEnvTemplates,
  listLinks,
  listNodes,
  listVolumes,
  mergeEnv,
  parseDotEnvDetailed,
  putEnv,
  replaceEnv,
  retryDeployment,
  rollbackDeployment,
  restartApplication,
  stopApplication,
  updateApplication,
  verifyDomain,
  type AppHealth,
  type Application,
  type DeployEnvironment,
  type Deployment,
  type EnvTemplate,
  type EnvVar,
  type AppDomain,
  type AppVolume,
  type ServiceLink,
} from "../../api";
import { EnvTemplates } from "../../env-templates";
import {
  elapsedSince,
  formatDuration,
  isInProgress,
  pillClass,
  stageExplanation,
  statusLabel,
  STAGES,
  stepClass,
  when,
  workerUnresponsiveNote,
} from "../../status";
import type { WorkerNode } from "../../api";

const TABS = [
  { id: "overview", label: "Overview" },
  { id: "deployments", label: "Deployments" },
  { id: "logs", label: "Logs" },
  { id: "environment", label: "Environment" },
  { id: "settings", label: "Settings" },
  { id: "advanced", label: "Advanced" },
] as const;

type Tab = (typeof TABS)[number]["id"];

function slotOf(d: Deployment): DeployEnvironment {
  return d.environment === "preview" ? "preview" : "production";
}

function previewSlug(slug: string): string {
  const s = slug.trim().toLowerCase();
  if (!s) {
    return "";
  }
  return s.endsWith("-preview") ? s : `${s}-preview`;
}

export default function ApplicationPage() {
  const params = useParams<{ id: string }>();
  const router = useRouter();
  const id = params.id;
  const [tab, setTab] = useState<Tab>("overview");
  const [slot, setSlot] = useState<DeployEnvironment>("production");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [app, setApp] = useState<Application | null>(null);
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [nodes, setNodes] = useState<WorkerNode[]>([]);
  const [now, setNow] = useState(() => Date.now());
  const [envFilter, setEnvFilter] = useState<"all" | DeployEnvironment>("all");
  const [statusFilter, setStatusFilter] = useState("all");
  const [rangeFilter, setRangeFilter] = useState<"all" | "day" | "week" | "month">("all");
  const [historyLimit, setHistoryLimit] = useState(25);
  const [logQuery, setLogQuery] = useState("");
  const [env, setEnv] = useState<EnvVar[]>([]);
  const [repoEnv, setRepoEnv] = useState<EnvTemplate[] | null>(null);
  const [links, setLinks] = useState<ServiceLink[]>([]);
  const [volumes, setVolumes] = useState<AppVolume[]>([]);
  const [domains, setDomains] = useState<AppDomain[]>([]);
  const [domainHost, setDomainHost] = useState("");
  const [siblings, setSiblings] = useState<Application[]>([]);
  const [linkTarget, setLinkTarget] = useState("");
  const [linkKey, setLinkKey] = useState("VITE_API_URL");
  const [volName, setVolName] = useState("data");
  const [volPath, setVolPath] = useState("/data");
  const [health, setHealth] = useState<AppHealth | null>(null);
  const [logs, setLogs] = useState("");
  const [streamStatus, setStreamStatus] = useState<"off" | "live" | "error">("off");
  const [name, setName] = useState("");
  const [repo, setRepo] = useState("");
  const [rootDirectory, setRootDirectory] = useState(".");
  const [healthPath, setHealthPath] = useState("/");
  const [localHost, setLocalHost] = useState("");
  const [memoryMB, setMemoryMB] = useState("");
  const [cpuMillis, setCpuMillis] = useState("");
  const [pidsLimit, setPidsLimit] = useState("");
  const [healthTimeout, setHealthTimeout] = useState("");
  const [envKey, setEnvKey] = useState("");
  const [envValue, setEnvValue] = useState("");
  const [envBulk, setEnvBulk] = useState("");
  const [envFileName, setEnvFileName] = useState("");
  const [showEnv, setShowEnv] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");

  async function refresh() {
    const [a, list, vars, svc, vols, doms] = await Promise.all([
      getApplication(id),
      listApplicationDeployments(id),
      listEnv(id),
      listLinks(id),
      listVolumes(id),
      listDomains(id),
    ]);
    setApp(a);
    setName(a.name);
    setRepo(a.repository_url);
    setRootDirectory(a.root_directory || ".");
    setHealthPath(a.health_path || "/");
    setLocalHost(a.local_host || "");
    setMemoryMB(a.memory_mb ? String(a.memory_mb) : "");
    setCpuMillis(a.cpu_millis ? String(a.cpu_millis) : "");
    setPidsLimit(a.pids_limit ? String(a.pids_limit) : "");
    setHealthTimeout(a.health_timeout_seconds ? String(a.health_timeout_seconds) : "");
    setDeployments(list);
    setEnv(vars);
    setLinks(svc);
    setVolumes(vols);
    setDomains(doms);
    try {
      const others = (await listApplications(a.project_id)).filter((x) => x.id !== a.id);
      setSiblings(others);
      if (!linkTarget && others[0]) {
        setLinkTarget(others[0].id);
      }
    } catch {
      setSiblings([]);
    }
    try {
      setHealth(await getApplicationHealth(id, slot));
    } catch {
      setHealth(null);
    }
  }

  useEffect(() => {
    let cancelled = false;
    setRepoEnv(null);
    void listEnvTemplates(id)
      .then((list) => {
        if (!cancelled) {
          setRepoEnv(list);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setRepoEnv([]);
        }
      });
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
    let cancelled = false;
    void getApplicationHealth(id, slot)
      .then((h) => {
        if (!cancelled) {
          setHealth(h);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setHealth(null);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [id, slot]);

  useEffect(() => {
    const pending = deployments.find((d) => isInProgress(d.status) && slotOf(d) === slot);
    const live = deployments.find((d) => d.status === "live" && slotOf(d) === slot);
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
          setHealth(await getApplicationHealth(id, slot));
        } catch {
          /* health may be stopped */
        }
      })();
    }, 1500);
    return () => window.clearInterval(t);
  }, [deployments, id, slot]);

  const slotPending = deployments.some((d) => isInProgress(d.status) && slotOf(d) === slot);

  useEffect(() => {
    if (!slotPending) {
      return;
    }
    const tick = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(tick);
  }, [slotPending]);

  useEffect(() => {
    if (!slotPending) {
      return;
    }
    let alive = true;
    async function loadNodes() {
      try {
        const list = await listNodes();
        if (alive) {
          setNodes(list);
        }
      } catch {
        // Heartbeat context is optional. A missing node list is not a dead worker.
      }
    }
    void loadNodes();
    const t = window.setInterval(() => void loadNodes(), 10000);
    return () => {
      alive = false;
      window.clearInterval(t);
    };
  }, [slotPending]);

  const liveId = deployments.find((d) => d.status === "live" && slotOf(d) === slot)?.id ?? "";

  useEffect(() => {
    if (tab !== "logs" || !liveId) {
      setStreamStatus("off");
      return;
    }
    const es = new EventSource(
      `/forge-api/applications/${id}/logs/stream?tail=200&environment=${encodeURIComponent(slot)}`,
    );
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
  }, [tab, id, liveId, slot]);

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
        memory_mb: memoryMB === "" ? 0 : Number(memoryMB),
        cpu_millis: cpuMillis === "" ? 0 : Number(cpuMillis),
        pids_limit: pidsLimit === "" ? 0 : Number(pidsLimit),
        health_timeout_seconds: healthTimeout === "" ? 0 : Number(healthTimeout),
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
      const d = await createApplicationDeployment(id, slot);
      setDeployments((cur) => [d, ...cur]);
      setSelectedId(d.id);
      setTab("deployments");
    } catch (err) {
      setError(err instanceof Error ? err.message : "deploy failed");
    } finally {
      setBusy("");
    }
  }

  async function onRequeue(deploymentId: string) {
    setBusy("requeue");
    setError("");
    try {
      const d = await retryDeployment(deploymentId);
      setDeployments((cur) => cur.map((x) => (x.id === d.id ? d : x)));
      setSelectedId(d.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : "re-queue failed");
    } finally {
      setBusy("");
    }
  }

  async function onStop() {
    const extra = inProgress
      ? " This also fails the attempt that is still running."
      : "";
    if (!window.confirm(`Stop the ${slot} environment? The serving release goes offline.${extra} Images and volumes are kept.`)) {
      return;
    }
    setBusy("stop");
    setError("");
    try {
      const d = await stopApplication(id, slot);
      setDeployments((cur) => {
        const next = cur.map((x) => (x.id === d.id ? d : x));
        return next.some((x) => x.id === d.id) ? next : [d, ...next];
      });
      try {
        setHealth(await getApplicationHealth(id, slot));
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

  async function onRestart() {
    if (
      !window.confirm(
        `Restart ${slot}? This queues a new deploy. The serving container stays up until the new one passes its health check.`,
      )
    ) {
      return;
    }
    setBusy("restart");
    setError("");
    try {
      const d = await restartApplication(id, slot);
      setDeployments((cur) => [d, ...cur.filter((x) => x.id !== d.id)]);
      setSelectedId(d.id);
      setTab("deployments");
      try {
        setHealth(await getApplicationHealth(id, slot));
      } catch {
        setHealth(null);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "restart failed");
    } finally {
      setBusy("");
    }
  }

  async function onLogs() {
    setBusy("logs");
    setError("");
    try {
      const got = await getApplicationLogs(id, 200, slot);
      setLogs(got.text || "(empty)");
    } catch (err) {
      setError(err instanceof Error ? err.message : "logs failed");
    } finally {
      setBusy("");
    }
  }

  async function onLink(e: FormEvent) {
    e.preventDefault();
    setBusy("link");
    setError("");
    try {
      await createLink(id, linkTarget, linkKey);
      setLinks(await listLinks(id));
    } catch (err) {
      setError(err instanceof Error ? err.message : "service link failed");
    } finally {
      setBusy("");
    }
  }

  async function onAddVolume(e: FormEvent) {
    e.preventDefault();
    setBusy("volume");
    setError("");
    try {
      await createVolume(id, volName, volPath);
      setVolumes(await listVolumes(id));
    } catch (err) {
      setError(err instanceof Error ? err.message : "volume failed");
    } finally {
      setBusy("");
    }
  }

  async function onRemoveVolume(volumeId: string) {
    setBusy("unvolume");
    setError("");
    try {
      await deleteVolume(id, volumeId);
      setVolumes(await listVolumes(id));
    } catch (err) {
      setError(err instanceof Error ? err.message : "remove volume failed");
    } finally {
      setBusy("");
    }
  }

  async function onAddDomain(e: FormEvent) {
    e.preventDefault();
    const host = domainHost.trim();
    if (!host || host.includes("://") || host.includes("/") || !host.includes(".")) {
      setError("Enter a public hostname such as app.example.com. Leave off https://. The app slug and .localhost names are not custom domains.");
      return;
    }
    setBusy("domain");
    setError("");
    try {
      await createDomain(id, host, slot);
      setDomains(await listDomains(id));
      setDomainHost("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "domain failed");
    } finally {
      setBusy("");
    }
  }

  async function onVerifyDomain(domainId: string) {
    setBusy("verify-" + domainId);
    setError("");
    try {
      await verifyDomain(id, domainId);
      setDomains(await listDomains(id));
    } catch (err) {
      setError(err instanceof Error ? err.message : "verify failed");
    } finally {
      setBusy("");
    }
  }

  async function onRemoveDomain(domainId: string) {
    setBusy("undomain");
    setError("");
    try {
      await deleteDomain(id, domainId);
      setDomains(await listDomains(id));
    } catch (err) {
      setError(err instanceof Error ? err.message : "remove domain failed");
    } finally {
      setBusy("");
    }
  }

  async function onUnlink(linkId: string) {
    setBusy("unlink");
    setError("");
    try {
      await deleteLink(id, linkId);
      setLinks(await listLinks(id));
    } catch (err) {
      setError(err instanceof Error ? err.message : "unlink failed");
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
    if (
      !window.confirm(
        "Roll back to this image? This queues a new attempt that skips the build. Traffic switches only after the health check passes.",
      )
    ) {
      return;
    }
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
  const inProgress = deployments.some((d) => isInProgress(d.status) && slotOf(d) === slot);
  const liveRow = deployments.find((d) => d.status === "live" && slotOf(d) === slot);
  const productionLive = deployments.find((d) => d.status === "live" && slotOf(d) === "production");
  const previewLive = deployments.find((d) => d.status === "live" && slotOf(d) === "preview");
  const latest = deployments.find((d) => slotOf(d) === slot);
  const selected =
    deployments.find((d) => d.id === selectedId) ?? (tab === "deployments" ? deployments[0] : null) ?? null;
  const running = health?.status === "healthy";
  const deployBlocked = inProgress;
  const productionSlug = app?.local_host || "";
  const hostSlug = slot === "preview" ? previewSlug(productionSlug) : productionSlug;
  const localURL = hostSlug ? `http://${hostSlug}.localhost:9080/` : "";
  const publicURL = health?.public_url || liveRow?.public_url || "";
  const pathURL = liveRow ? `http://127.0.0.1:9080/d/${liveRow.id}/` : "";
  const visitURL = localURL || publicURL || pathURL;
  const visitLabel = localURL
    ? `${hostSlug}.localhost:9080`
    : publicURL.includes(".localhost:")
      ? publicURL.replace(/^https?:\/\//, "").replace(/\/$/, "")
      : publicURL;
  const pending = deployments.find((d) => isInProgress(d.status) && slotOf(d) === slot);
  const healthLabel = health?.status ?? (liveRow ? "unknown" : "stopped");
  const slotLabel = liveRow ? (running ? "Serving" : healthLabel) : "Not serving";
  const deployStatus = pending
    ? statusLabel(pending.status)
    : latest
      ? statusLabel(latest.status)
      : "No deployments";
  const attemptNode = pending?.node_id ? nodes.find((n) => n.id === pending.node_id) ?? null : null;
  const attemptWorkerNote = pending ? workerUnresponsiveNote(attemptNode?.status, attemptNode?.name) : null;
  const selectedEnv = selected ? slotOf(selected) : slot;
  const selectedPending = deployments.some((d) => isInProgress(d.status) && slotOf(d) === selectedEnv);
  const rangeMs = rangeFilter === "day" ? 86400000 : rangeFilter === "week" ? 7 * 86400000 : rangeFilter === "month" ? 30 * 86400000 : 0;
  const filteredDeployments = deployments.filter((d) => {
    if (envFilter !== "all" && slotOf(d) !== envFilter) {
      return false;
    }
    if (statusFilter !== "all" && d.status !== statusFilter) {
      return false;
    }
    if (rangeMs > 0) {
      const started = Date.parse(d.created_at);
      if (!Number.isNaN(started) && now - started > rangeMs) {
        return false;
      }
    }
    return true;
  });
  const visibleDeployments = filteredDeployments.slice(0, historyLimit);

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
            <span className={liveRow && running ? "pill live" : "pill stopped"}>
              {liveRow && running ? "Serving" : liveRow ? "Serving · check failed" : "Not serving"}
            </span>
            {pending ? (
              <span className={pillClass(pending.status)}>
                Attempt · {statusLabel(pending.status)}
                {elapsedSince(pending.created_at, now) ? ` · ${elapsedSince(pending.created_at, now)}` : ""}
              </span>
            ) : latest && latest.status !== "live" ? (
              <span className={pillClass(latest.status)}>Latest attempt · {statusLabel(latest.status)}</span>
            ) : null}
            <span className="repo-url" title={app?.repository_url}>
              {app?.repository_url}
            </span>
            {app?.platform_url ? (
              <a href={app.platform_url} target="_blank" rel="noreferrer">
                {app.platform_url.replace(/\/$/, "")}
              </a>
            ) : null}
          </p>
        </div>
        <div className="actions-row">
          <label className="slot-label" htmlFor="deploy-slot">
            Target
            <select
              id="deploy-slot"
              className="env-select"
              value={slot}
              onChange={(e) => {
                setSlot(e.target.value === "preview" ? "preview" : "production");
                setLogs("");
                setHealth(null);
              }}
            >
              <option value="production">Production</option>
              <option value="preview">Preview</option>
            </select>
          </label>
          <button className="btn-primary" type="button" onClick={onDeploy} disabled={!!busy || deployBlocked}>
            {busy === "deploy" ? "Queueing…" : deployBlocked ? "Deploy in progress" : liveRow ? `Redeploy ${slot}` : `Deploy ${slot}`}
          </button>
          {liveRow && running && visitURL ? (
            <a className="btn btn-ghost" href={visitURL} target="_blank" rel="noreferrer">
              Open
            </a>
          ) : null}
          <button className="btn-ghost" type="button" onClick={onRestart} disabled={!!busy || !liveRow || inProgress}>
            {busy === "restart" ? "Restarting…" : "Restart"}
          </button>
          <button className="btn-danger" type="button" onClick={onStop} disabled={!!busy || (!liveRow && !pending)}>
            {busy === "stop" ? "Stopping…" : "Stop"}
          </button>
        </div>
      </div>

      {sample ? (
        <div className="banner">
          {liveRow && running
            ? `This ${slot} environment is live. Redeploy starts a new container beside it and switches traffic after health checks. Production and preview are isolated.`
            : "github.com/example and forge://hello use the bundled sample (no GitHub clone). Keep the worker running, then Deploy."}
        </div>
      ) : pending && liveRow ? (
        <div className="banner">
          A new {slot} attempt is {statusLabel(pending.status).toLowerCase()}. The current release keeps serving until that attempt passes its health check.
        </div>
      ) : pending ? (
        <div className="banner">
          Nothing is serving in {slot}. This attempt is {statusLabel(pending.status).toLowerCase()} and has not passed a health check.
        </div>
      ) : latest && latest.status === "failed" && liveRow ? (
        <div className="banner">The latest {slot} attempt failed. The previous healthy release is still serving.</div>
      ) : latest && latest.status === "failed" ? (
        <div className="banner">The latest {slot} attempt failed. Nothing is serving in this environment.</div>
      ) : liveRow && running ? (
        <div className="banner">This {slot} environment is serving. Redeploy starts a new attempt beside it. A failed health check keeps this release.</div>
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
              <div className="stat-value">{productionLive ? "Live" : "Offline"}</div>
            </div>
            <div className="stat">
              <span className="stat-label">Preview</span>
              <div className="stat-value">{previewLive ? "Live" : "Offline"}</div>
            </div>
            <div className="stat">
              <span className="stat-label">{slot === "preview" ? "Preview" : "Production"} slot</span>
              <div className="stat-value">{slotLabel}</div>
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
            <h2>{pending ? "Current attempt" : "Latest attempt"}</h2>
            {(pending ?? latest) ? (
              <>
                <div className="deploy-top">
                  <span className={pillClass((pending ?? latest)!.status)}>{statusLabel((pending ?? latest)!.status)}</span>
                  <span className="deploy-meta" style={{ margin: 0 }}>
                    Started {when((pending ?? latest)!.created_at)}
                    {isInProgress((pending ?? latest)!.status)
                      ? ` · elapsed ${elapsedSince((pending ?? latest)!.created_at, now) || "under 1 ms"}`
                      : (pending ?? latest)!.duration_ms
                        ? ` · ${formatDuration((pending ?? latest)!.duration_ms)}`
                        : ""}
                    {(pending ?? latest)!.updated_at ? ` · updated ${when((pending ?? latest)!.updated_at)}` : ""}
                  </span>
                </div>
                <div className="pipeline" aria-label="deployment stages">
                  {STAGES.map((stage) => (
                    <span key={stage} className={stepClass(stage, (pending ?? latest)!)}>
                      {statusLabel(stage)}
                    </span>
                  ))}
                </div>
                <p className="hint">{stageExplanation(pending ?? latest!)}</p>
                {attemptWorkerNote ? <p className="hint">{attemptWorkerNote}</p> : null}
                {(pending ?? latest)!.error_message ? <div className="err-box">{(pending ?? latest)!.error_message}</div> : null}
                <div className="actions-row">
                  <button
                    className="btn-ghost"
                    type="button"
                    onClick={() => {
                      const focus = pending ?? latest;
                      if (!focus) {
                        return;
                      }
                      setSelectedId(focus.id);
                      setTab("deployments");
                    }}
                  >
                    View details
                  </button>
                  {liveRow && (visitURL || liveRow.public_url) ? (
                    <a className="btn btn-ghost" href={visitURL || liveRow.public_url} target="_blank" rel="noreferrer">
                      Open app
                    </a>
                  ) : null}
                </div>
              </>
            ) : (
              <p className="empty">No deployments yet. Deploy to start the first build.</p>
            )}
          </div>

          {liveRow && pending && liveRow.id !== pending.id ? (
            <div className="card">
              <h2>Serving release</h2>
              <p className="hint">
                Started {when(liveRow.created_at)}. This {slot} release is still the one that can receive traffic. The attempt above replaces it only after a passing health check.
              </p>
            </div>
          ) : null}

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
                    {slotOf(d) === "preview" ? " · Preview" : " · Production"}
                    {d.rollback_of ? " · Rollback" : ""}
                    {d.commit_sha ? ` · ${d.commit_sha.slice(0, 7)}` : ""}
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
          <div className="stack">
            <div className="filter-bar">
              <label>
                Environment
                <select value={envFilter} onChange={(e) => setEnvFilter(e.target.value === "preview" || e.target.value === "production" ? e.target.value : "all")}>
                  <option value="all">All</option>
                  <option value="production">Production</option>
                  <option value="preview">Preview</option>
                </select>
              </label>
              <label>
                Status
                <select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
                  <option value="all">All</option>
                  {[...STAGES, "failed", "stopped"].map((s) => (
                    <option key={s} value={s}>
                      {statusLabel(s)}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Started
                <select
                  value={rangeFilter}
                  onChange={(e) => setRangeFilter(e.target.value === "day" || e.target.value === "week" || e.target.value === "month" ? e.target.value : "all")}
                >
                  <option value="all">Any time</option>
                  <option value="day">Last 24 hours</option>
                  <option value="week">Last 7 days</option>
                  <option value="month">Last 30 days</option>
                </select>
              </label>
            </div>
          <div className="deploy-split">
            <div className="deploy-list">
              {visibleDeployments.length === 0 ? <p className="empty">No deployments match these filters.</p> : null}
              {visibleDeployments.map((d) => (
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
                    {slotOf(d) === "preview" ? " · Preview" : " · Production"}
                    {d.rollback_of ? " · Rollback" : ""}
                    {d.commit_sha ? ` · ${d.commit_sha.slice(0, 7)}` : ""}
                    {d.error_message ? ` · ${d.error_message.slice(0, 80)}` : ""}
                  </span>
                </button>
              ))}
              {filteredDeployments.length > visibleDeployments.length ? (
                <button className="btn-ghost" type="button" onClick={() => setHistoryLimit((n) => n + 25)}>
                  Show older
                </button>
              ) : null}
            </div>
            <div className="deploy-detail">
              {selected ? (
                <DeploymentDetail
                  d={selected}
                  busy={busy}
                  now={now}
                  logQuery={logQuery}
                  onLogQuery={setLogQuery}
                  workerNote={
                    isInProgress(selected.status)
                      ? workerUnresponsiveNote(
                          nodes.find((n) => n.id === selected.node_id)?.status,
                          nodes.find((n) => n.id === selected.node_id)?.name,
                        )
                      : null
                  }
                  canRollback={!!selected.image_name && !selectedPending}
                  onRollback={onRollback}
                  onRequeue={onRequeue}
                />
              ) : (
                <p className="empty">Select a deployment to see build status and logs.</p>
              )}
            </div>
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
              ? `Live stream of the ${slot} container. Refresh still takes a snapshot.`
              : streamStatus === "error"
                ? "Stream ended. Use Refresh for a snapshot, or reopen this tab after the slot is live."
                : `Runtime logs appear after a successful ${slot} deploy. A live app in this slot streams automatically.`}
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
          <EnvTemplates applicationId={id} templates={repoEnv} onSaved={() => void refresh()} />
          {repoEnv === null ? null : env.length === 0 && repoEnv.length === 0 ? (
            <p className="empty" style={{ marginTop: "0.85rem" }}>
              No variables yet.
            </p>
          ) : env.length === 0 ? null : (
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
          {repoEnv === null ? null : (
          <>
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
          </>
          )}
        </div>
      ) : null}

      {tab === "advanced" ? (
        <div className="card">
          <h2>Service links</h2>
          <p className="hint" style={{ marginTop: 0 }}>
            Give this app another app&apos;s address as an environment variable on the next deploy.
          </p>
          {links.length === 0 ? (
            <p className="empty">None yet. Add a sibling below.</p>
          ) : (
            <div className="project-list">
              {links.map((l) => (
                <div className="project-row" key={l.id}>
                  <div>
                    <strong>{l.env_key}</strong>
                    <p className="empty">
                      {l.target_name || l.target_application_id}
                      {l.url ? ` → ${l.url}` : ""}
                    </p>
                  </div>
                  <button className="btn-ghost" type="button" onClick={() => void onUnlink(l.id)} disabled={!!busy}>
                    Remove
                  </button>
                </div>
              ))}
            </div>
          )}
          <form onSubmit={(e) => void onLink(e)} style={{ marginTop: "1rem" }}>
            <div className="form-grid">
              <div>
                <label htmlFor="link-target">Target application</label>
                <select
                  id="link-target"
                  value={linkTarget}
                  onChange={(e) => setLinkTarget(e.target.value)}
                  disabled={siblings.length === 0}
                >
                  {siblings.length === 0 ? <option value="">No other apps in this project</option> : null}
                  {siblings.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name}
                      {a.local_host ? ` (${a.local_host}.localhost)` : ""}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label htmlFor="link-key">Env key</label>
                <input
                  id="link-key"
                  value={linkKey}
                  onChange={(e) => setLinkKey(e.target.value)}
                  placeholder="VITE_API_URL"
                />
              </div>
            </div>
            <div className="actions" style={{ marginTop: "0.75rem" }}>
              <button className="btn-primary" type="submit" disabled={!!busy || !linkTarget}>
                {busy === "link" ? "Saving…" : "Add link"}
              </button>
            </div>
          </form>
        </div>
      ) : null}

      {tab === "advanced" ? (
        <div className="card">
          <h2>Persistent volumes</h2>
          <p className="hint" style={{ marginTop: 0 }}>
            A named disk that stays across deploys. Host folders are not allowed. Stop the app before deleting a disk.
          </p>
          {volumes.length === 0 ? (
            <p className="empty">None yet. Add a mount below (for example <code>/data</code>).</p>
          ) : (
            <div className="project-list">
              {volumes.map((v) => (
                <div className="project-row" key={v.id}>
                  <div>
                    <strong>{v.name}</strong>
                    <p className="empty">
                      {v.mount_path}
                      {v.docker_name ? ` · prod ${v.docker_name}` : ""}
                    </p>
                    {v.preview_docker_name ? <p className="empty">preview {v.preview_docker_name}</p> : null}
                  </div>
                  <button className="btn-ghost" type="button" onClick={() => void onRemoveVolume(v.id)} disabled={!!busy}>
                    Remove
                  </button>
                </div>
              ))}
            </div>
          )}
          <form onSubmit={(e) => void onAddVolume(e)} style={{ marginTop: "1rem" }}>
            <div className="form-grid">
              <div>
                <label htmlFor="vol-name">Name</label>
                <input
                  id="vol-name"
                  value={volName}
                  onChange={(e) => setVolName(e.target.value)}
                  placeholder="data"
                />
              </div>
              <div>
                <label htmlFor="vol-path">Container path</label>
                <input
                  id="vol-path"
                  value={volPath}
                  onChange={(e) => setVolPath(e.target.value)}
                  placeholder="/data"
                />
              </div>
            </div>
            <div className="actions" style={{ marginTop: "0.75rem" }}>
              <button className="btn-primary" type="submit" disabled={!!busy}>
                {busy === "volume" ? "Saving…" : "Add volume"}
              </button>
            </div>
          </form>
        </div>
      ) : null}

      {tab === "advanced" ? (
        <div className="card">
          <h2>Custom domains</h2>
          <p className="hint" style={{ marginTop: 0 }}>
            Add a hostname you own, then publish the TXT record Forge shows and press Verify.
          </p>
          {domains.length === 0 ? (
            <p className="empty">None yet. Add a hostname below (for example <code>app.example.com</code>).</p>
          ) : (
            <div className="project-list">
              {domains.map((d) => (
                <div className="project-row" key={d.id}>
                  <div>
                    <strong>{d.hostname}</strong>
                    <p className="empty">
                      {d.environment} · {d.status}
                      {d.status === "active" ? " · https://" + d.hostname : ""}
                    </p>
                    {d.origin !== "platform" && d.status !== "active" ? (
                      <p className="empty">
                        TXT {d.txt_name} = {d.txt_value}
                      </p>
                    ) : null}
                    {d.origin === "platform" ? (
                      <p className="empty">Assigned from the application slug. Change the slug to rename it.</p>
                    ) : null}
                  </div>
                  <div className="actions">
                    {d.origin !== "platform" && d.status !== "active" ? (
                      <button
                        className="btn-ghost"
                        type="button"
                        onClick={() => void onVerifyDomain(d.id)}
                        disabled={!!busy}
                      >
                        {busy === "verify-" + d.id ? "Checking…" : "Verify DNS"}
                      </button>
                    ) : null}
                    {d.origin === "platform" ? null : (
                      <button className="btn-ghost" type="button" onClick={() => void onRemoveDomain(d.id)} disabled={!!busy}>
                        Remove
                      </button>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
          <form onSubmit={(e) => void onAddDomain(e)} style={{ marginTop: "1rem" }}>
            <div className="form-grid">
              <div>
                <label htmlFor="domain-host">Hostname</label>
                <input
                  id="domain-host"
                  value={domainHost}
                  onChange={(e) => setDomainHost(e.target.value)}
                  placeholder="app.example.com"
                />
              </div>
            </div>
            <p className="hint">
              Attached to the currently selected environment ({slot}). This box is only for a name you own in DNS, such as app.example.com.
            </p>
            {error ? <p className="error">{error}</p> : null}
            <div className="actions" style={{ marginTop: "0.75rem" }}>
              <button className="btn-primary" type="submit" disabled={!!busy}>
                {busy === "domain" ? "Saving…" : "Add domain"}
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
            optional — unnamed apps still deploy. Production uses <code>{`{slug}`}.localhost</code>.
            Preview uses <code>{`{slug}-preview`}.localhost</code>; do not set a slug ending in
            <code>-preview</code>. Health path should be a route that returns 2xx (APIs that
            404 on / need something like /api/customers).
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
            <div>
              <label htmlFor="memory-mb">Memory (MiB)</label>
              <input
                id="memory-mb"
                inputMode="numeric"
                value={memoryMB}
                onChange={(e) => setMemoryMB(e.target.value)}
                placeholder="256"
              />
            </div>
            <div>
              <label htmlFor="cpu-millis">CPU (milli-CPU)</label>
              <input
                id="cpu-millis"
                inputMode="numeric"
                value={cpuMillis}
                onChange={(e) => setCpuMillis(e.target.value)}
                placeholder="500"
              />
            </div>
            <div>
              <label htmlFor="pids-limit">Process limit</label>
              <input
                id="pids-limit"
                inputMode="numeric"
                value={pidsLimit}
                onChange={(e) => setPidsLimit(e.target.value)}
                placeholder="256"
              />
            </div>
            <div>
              <label htmlFor="health-timeout">Health timeout (seconds)</label>
              <input
                id="health-timeout"
                inputMode="numeric"
                value={healthTimeout}
                onChange={(e) => setHealthTimeout(e.target.value)}
                placeholder="45"
              />
            </div>
          </div>
          <p className="hint">
            Blank keeps the current defaults: 256 MiB, 0.5 CPU, 256 processes, and a 45 second
            readiness check. Allowed ranges are 64–2048 MiB, 100–2000 milli-CPU, 32–1024 processes,
            and 5–180 seconds. A container that exits before that check is a startup failure and
            does not replace the last healthy release.
          </p>
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
  now,
  logQuery,
  onLogQuery,
  workerNote,
  canRollback,
  onRollback,
  onRequeue,
}: {
  d: Deployment;
  busy: string;
  now: number;
  logQuery: string;
  onLogQuery: (value: string) => void;
  workerNote: string | null;
  canRollback: boolean;
  onRollback: (id: string) => void;
  onRequeue: (id: string) => void;
}) {
  const logText = d.build_log ?? "";
  const shownLog = logQuery.trim()
    ? logText
        .split("\n")
        .filter((line) => line.toLowerCase().includes(logQuery.trim().toLowerCase()))
        .join("\n")
    : logText;
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
          {d.status === "queued" ? (
            <button className="btn-primary" type="button" disabled={!!busy} onClick={() => onRequeue(d.id)}>
              {busy === "requeue" ? "Re-queueing…" : "Re-queue"}
            </button>
          ) : null}
          {canRollback ? (
            <button
              className={d.status === "failed" ? "btn-primary" : "btn-ghost"}
              type="button"
              disabled={!!busy}
              onClick={() => onRollback(d.id)}
            >
              {busy === "rollback" ? "Queueing…" : "Rollback to this image"}
            </button>
          ) : null}
        </div>
      </div>
      {logText.includes("build cache hit") ? (
        <p className="hint">Build cache hit. Docker build was skipped. Health checks still run before traffic switches.</p>
      ) : logText.includes("build cache miss") ? (
        <p className="hint">Build cache miss. This attempt built a new image. A later attempt with the same inputs can reuse it.</p>
      ) : null}
      <p className="deploy-meta">
        Started {when(d.created_at)}
        {isInProgress(d.status) ? ` · elapsed ${elapsedSince(d.created_at, now) || "under 1 ms"}` : ""}
        {d.updated_at ? ` · updated ${when(d.updated_at)}` : ""}
        {d.duration_ms ? ` · ${formatDuration(d.duration_ms)}` : ""}
        {d.environment ? ` · ${d.environment}` : ""}
        {d.failed_stage ? ` · failed at ${d.failed_stage}` : ""}
        {d.rollback_of ? " · Rollback" : ""}
        {d.commit_sha ? ` · ${d.commit_sha.slice(0, 7)}` : ""}
        {d.commit_author ? ` · ${d.commit_author}` : ""}
      </p>
      {d.commit_message ? <p className="empty">{d.commit_message}</p> : null}
      <div className="pipeline" aria-label="deployment stages">
        {STAGES.map((stage) => (
          <span key={stage} className={stepClass(stage, d)}>
            {statusLabel(stage)}
          </span>
        ))}
      </div>
      {stageExplanation(d) ? <p className="hint">{stageExplanation(d)}</p> : null}
      {workerNote ? <p className="hint">{workerNote}</p> : null}
      {d.status === "queued" ? (
        <p className="hint">
          Re-queue sends this same attempt again. Use it when the worker never received the job. It does not start a second copy of an attempt that is already running.
        </p>
      ) : null}
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
      {logText ? (
        <>
          <h2 className="section-gap">Build log</h2>
          <p className="hint">Builder output for this attempt. Application runtime logs are on the Logs tab.</p>
          <label className="log-filter" htmlFor="log-filter">
            Filter log lines
            <input
              id="log-filter"
              value={logQuery}
              onChange={(e) => onLogQuery(e.target.value)}
              placeholder="error, npm, health"
            />
          </label>
          {shownLog ? <pre className="log-box">{shownLog}</pre> : <p className="empty">No lines match that filter.</p>}
        </>
      ) : (
        <p className="empty">
          {d.status === "failed" && (d.failed_stage === "detecting" || d.failed_stage === "queued")
            ? "No build log. This attempt failed before Docker started."
            : isInProgress(d.status) && d.status !== "building"
              ? "No build log yet. Docker has not produced output for this stage."
              : d.status === "building"
                ? "No build log yet. It is saved when the build stage finishes."
                : "No build log for this deployment."}
        </p>
      )}
    </>
  );
}
