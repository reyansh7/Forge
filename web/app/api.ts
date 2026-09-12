export type Project = {
  id: string;
  name: string;
  repository_url: string;
  created_at: string;
  updated_at: string;
};

export type Application = {
  id: string;
  project_id: string;
  name: string;
  repository_url: string;
  root_directory: string;
  health_path: string;
  local_host: string;
  created_at: string;
  updated_at: string;
};

export type Deployment = {
  id: string;
  project_id: string;
  application_id: string;
  status: string;
  failed_stage?: string;
  error_message?: string;
  runtime_kind?: string;
  public_url?: string;
  image_name?: string;
  build_log?: string;
  rollback_of?: string;
  local_host?: string;
  duration_ms?: number;
  created_at: string;
  updated_at: string;
};

export type EnvVar = {
  key: string;
  value: string;
};

export type AppLogs = {
  application_id: string;
  deployment_id: string;
  tail: number;
  text: string;
};

export type AppHealth = {
  application_id: string;
  deployment_id?: string;
  status: string;
  public_url?: string;
};

const TOKEN_KEY = "forge_session_token";

export function setSessionToken(token: string) {
  sessionStorage.setItem(TOKEN_KEY, token);
}

export function clearSession() {
  sessionStorage.removeItem(TOKEN_KEY);
}

function sessionToken(): string {
  if (typeof window === "undefined") {
    return "";
  }
  return sessionStorage.getItem(TOKEN_KEY) || "";
}

function isAnonymousAuthPath(path: string): boolean {
  return (
    path.includes("/auth/login") ||
    path.includes("/auth/signup") ||
    path.includes("/auth/bootstrap") ||
    path.includes("/auth/logout") ||
    path.includes("/auth/status")
  );
}

// dropBrowserSession clears the previous operator before a new login or
// signup. The HttpOnly cookie is revoked via logout; sessionStorage
// Bearer is dropped so it cannot keep the old user after Set-Cookie.
async function dropBrowserSession(): Promise<void> {
  clearSession();
  try {
    await fetch("/forge-api/auth/logout", { method: "POST", credentials: "include" });
  } catch {
    // The form must still submit if the API is briefly unreachable.
  }
}

function apiFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  const token = sessionToken();
  // Do not attach a previous operator's Bearer to login/signup. The
  // server prefers the session cookie when both are present; a stale
  // header would still confuse curl-style clients that send only Bearer.
  if (token && !isAnonymousAuthPath(path)) {
    headers.set("Authorization", `Bearer ${token}`);
  }
  return fetch(path, { ...init, headers, credentials: "include" });
}

async function parse<T>(res: Response): Promise<T> {
  if (res.status === 401 && typeof window !== "undefined" && !window.location.pathname.startsWith("/login")) {
    clearSession();
    window.location.href = "/login";
  }
  const text = await res.text();
  let body: unknown = null;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      throw new Error(text || res.statusText);
    }
  }
  if (!res.ok) {
    const err = body as { error?: string } | null;
    throw new Error(err?.error || res.statusText);
  }
  return body as T;
}

export type AuthUser = {
  id: string;
  username: string;
};

export type AuthSession = {
  token: string;
  user: AuthUser;
};

export function authStatus(): Promise<{ bootstrap_required: boolean }> {
  return apiFetch("/forge-api/auth/status").then((r) => parse<{ bootstrap_required: boolean }>(r));
}

export async function bootstrap(username: string, password: string): Promise<AuthSession> {
  await dropBrowserSession();
  return apiFetch("/forge-api/auth/bootstrap", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  }).then((r) => parse<AuthSession>(r));
}

export async function signup(username: string, password: string, password_confirm: string): Promise<AuthSession> {
  await dropBrowserSession();
  return apiFetch("/forge-api/auth/signup", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password, password_confirm }),
  }).then((r) => parse<AuthSession>(r));
}

export async function login(username: string, password: string): Promise<AuthSession> {
  await dropBrowserSession();
  return apiFetch("/forge-api/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  }).then((r) => parse<AuthSession>(r));
}

export function logout(): Promise<void> {
  return apiFetch("/forge-api/auth/logout", { method: "POST" }).then(async (r) => {
    if (!r.ok && r.status !== 204) {
      return parse<void>(r);
    }
  });
}

export function me(): Promise<AuthUser> {
  return apiFetch("/forge-api/auth/me").then((r) => parse<AuthUser>(r));
}

export function listProjects(): Promise<Project[]> {
  return apiFetch("/forge-api/projects").then((r) => parse<Project[]>(r));
}

export function createProject(name: string, repository_url: string): Promise<Project> {
  return apiFetch("/forge-api/projects", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, repository_url }),
  }).then((r) => parse<Project>(r));
}

export function getProject(id: string): Promise<Project> {
  return apiFetch(`/forge-api/projects/${id}`).then((r) => parse<Project>(r));
}

export function listApplications(projectId: string): Promise<Application[]> {
  return apiFetch(`/forge-api/projects/${projectId}/applications`).then((r) => parse<Application[]>(r));
}

export function createApplication(
  projectId: string,
  name: string,
  repository_url: string,
): Promise<Application> {
  return apiFetch(`/forge-api/projects/${projectId}/applications`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, repository_url }),
  }).then((r) => parse<Application>(r));
}

export function getApplication(id: string): Promise<Application> {
  return apiFetch(`/forge-api/applications/${id}`).then((r) => parse<Application>(r));
}

export function updateApplication(
  id: string,
  body: {
    name: string;
    repository_url: string;
    root_directory: string;
    health_path: string;
    local_host: string;
  },
): Promise<Application> {
  return apiFetch(`/forge-api/applications/${id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  }).then((r) => parse<Application>(r));
}

export function deleteApplication(id: string): Promise<void> {
  return apiFetch(`/forge-api/applications/${id}`, { method: "DELETE" }).then((r) => {
    if (!r.ok && r.status !== 204) {
      return parse<void>(r);
    }
  });
}

export function listDeployments(projectId: string): Promise<Deployment[]> {
  return apiFetch(`/forge-api/projects/${projectId}/deployments`).then((r) => parse<Deployment[]>(r));
}

export function listApplicationDeployments(applicationId: string): Promise<Deployment[]> {
  return apiFetch(`/forge-api/applications/${applicationId}/deployments`).then((r) =>
    parse<Deployment[]>(r),
  );
}

export function createDeployment(projectId: string): Promise<Deployment> {
  return apiFetch(`/forge-api/projects/${projectId}/deployments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({}),
  }).then((r) => parse<Deployment>(r));
}

export function createApplicationDeployment(applicationId: string): Promise<Deployment> {
  return apiFetch(`/forge-api/applications/${applicationId}/deployments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({}),
  }).then((r) => parse<Deployment>(r));
}

export function getDeployment(id: string): Promise<Deployment> {
  return apiFetch(`/forge-api/deployments/${id}`).then((r) => parse<Deployment>(r));
}

export function listEnv(applicationId: string): Promise<EnvVar[]> {
  return apiFetch(`/forge-api/applications/${applicationId}/env`).then((r) => parse<EnvVar[]>(r));
}

export function putEnv(applicationId: string, key: string, value: string): Promise<EnvVar> {
  return apiFetch(`/forge-api/applications/${applicationId}/env`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ key, value }),
  }).then((r) => parse<EnvVar>(r));
}

export function deleteEnv(applicationId: string, key: string): Promise<void> {
  return apiFetch(`/forge-api/applications/${applicationId}/env/${encodeURIComponent(key)}`, {
    method: "DELETE",
  }).then((r) => {
    if (!r.ok && r.status !== 204) {
      return parse<void>(r);
    }
  });
}

export function replaceEnv(applicationId: string, vars: EnvVar[]): Promise<EnvVar[]> {
  return apiFetch(`/forge-api/applications/${applicationId}/env/bulk`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ vars }),
  }).then((r) => parse<EnvVar[]>(r));
}

export function rollbackDeployment(sourceId: string): Promise<Deployment> {
  return apiFetch(`/forge-api/deployments/${sourceId}/rollback`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({}),
  }).then((r) => parse<Deployment>(r));
}

/** Split a pasted KEY=VALUE block. Lines starting with # are ignored. */
export function parseDotEnv(text: string): EnvVar[] {
  const out: EnvVar[] = [];
  for (const line of text.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) {
      continue;
    }
    const eq = trimmed.indexOf("=");
    if (eq < 1) {
      continue;
    }
    out.push({ key: trimmed.slice(0, eq).trim(), value: trimmed.slice(eq + 1) });
  }
  return out;
}

export type ObserveMetrics = {
  http_requests_total: number;
  http_errors_total: number;
  deploys_queued_total: number;
  deployments_total: number;
  deployments_live: number;
  deployments_failed: number;
  last_deploy_duration_ms: number;
  containers_running: number;
  containers_checked: number;
};

export function getMetrics(): Promise<ObserveMetrics> {
  return apiFetch("/forge-api/metrics").then((r) => parse<ObserveMetrics>(r));
}

export function getApplicationLogs(applicationId: string, tail = 100): Promise<AppLogs> {
  return apiFetch(`/forge-api/applications/${applicationId}/logs?tail=${tail}`).then((r) =>
    parse<AppLogs>(r),
  );
}

export function getApplicationHealth(applicationId: string): Promise<AppHealth> {
  return apiFetch(`/forge-api/applications/${applicationId}/health`).then((r) => parse<AppHealth>(r));
}

export function stopApplication(applicationId: string): Promise<Deployment> {
  return apiFetch(`/forge-api/applications/${applicationId}/stop`, { method: "POST" }).then((r) =>
    parse<Deployment>(r),
  );
}

export type Health = {
  status: string;
  postgres: string;
  redis: string;
};

export function getHealth(): Promise<Health> {
  return apiFetch("/forge-api/health").then((r) => parse<Health>(r));
}

export function isBundledSample(url: string): boolean {
  const raw = url.trim().toLowerCase();
  if (raw === "forge://hello") {
    return true;
  }
  try {
    const u = new URL(url);
    return u.hostname === "github.com" && u.pathname.split("/").filter(Boolean)[0] === "example";
  } catch {
    return false;
  }
}
