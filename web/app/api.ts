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
  memory_mb?: number;
  cpu_millis?: number;
  pids_limit?: number;
  health_timeout_seconds?: number;
  platform_url?: string;
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
  runtime_type?: string;
  port_source?: string;
  host_port?: number;
  listen_port?: number;
  container_id?: string;
  container_name?: string;
  public_url?: string;
  image_name?: string;
  build_log?: string;
  rollback_of?: string;
  local_host?: string;
  environment?: string;
  node_id?: string;
  commit_sha?: string;
  commit_message?: string;
  commit_author?: string;
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
  environment?: string;
  tail: number;
  text: string;
};

export type AppHealth = {
  application_id: string;
  deployment_id?: string;
  environment?: string;
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

export type PlanDiagnosis = {
  code: string;
  stage: string;
  evidence: string;
  confidence: string;
  recommendation: string;
  risk: string;
  blocking: boolean;
};

export type PlanService = {
  name: string;
  root: string;
  package?: string;
  kind?: string;
  framework?: string;
  runtime?: string;
  port?: number;
  deployable: boolean;
  detail: string;
  diagnoses?: PlanDiagnosis[];
};

export function projectPlan(id: string): Promise<PlanService[]> {
  return apiFetch(`/forge-api/projects/${id}/plan`).then((r) => parse<PlanService[]>(r));
}

export type ProjectDeployService = {
  name: string;
  application_id: string;
  deployment_id?: string;
  root: string;
  package?: string;
  port?: number;
  status: string;
  error?: string;
  public_url?: string;
  internal_url?: string;
  depends_on?: string[];
  generated_env?: string[];
};

export type ProjectDeployLink = {
  source: string;
  target: string;
  env_key: string;
};

export type ProjectDeploy = {
  id: string;
  project_id: string;
  status: string;
  error_message?: string;
  note?: string;
  attention?: string;
  services: ProjectDeployService[];
  links: ProjectDeployLink[];
  created_at: string;
};

export function deployProject(id: string, services?: string[]): Promise<ProjectDeploy> {
  return apiFetch(`/forge-api/projects/${id}/deploy`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(services && services.length > 0 ? { services } : {}),
  }).then((r) => parse<ProjectDeploy>(r));
}

export function listProjectDeploys(id: string): Promise<ProjectDeploy[]> {
  return apiFetch(`/forge-api/projects/${id}/deploys`).then((r) => parse<ProjectDeploy[]>(r));
}

export function getProjectDeploy(projectId: string, deployId: string): Promise<ProjectDeploy> {
  return apiFetch(`/forge-api/projects/${projectId}/deploys/${deployId}`).then((r) => parse<ProjectDeploy>(r));
}

export function cancelProjectDeploy(projectId: string, deployId: string): Promise<ProjectDeploy> {
  return apiFetch(`/forge-api/projects/${projectId}/deploys/${deployId}/cancel`, { method: "POST" }).then((r) =>
    parse<ProjectDeploy>(r),
  );
}

export function rollbackProjectDeploy(projectId: string, deployId: string): Promise<ProjectDeploy> {
  return apiFetch(`/forge-api/projects/${projectId}/deploys/${deployId}/rollback`, { method: "POST" }).then((r) =>
    parse<ProjectDeploy>(r),
  );
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
    memory_mb?: number;
    cpu_millis?: number;
    pids_limit?: number;
    health_timeout_seconds?: number;
  },
): Promise<Application> {
  return apiFetch(`/forge-api/applications/${id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  }).then((r) => parse<Application>(r));
}

export type EnvPasteResult = {
  directory: string;
  application_id?: string;
  name?: string;
  saved: { key: string; class: string }[];
  kept: string[];
  issues: string[];
  warnings: string[];
};

export function saveProjectEnv(
  projectId: string,
  services: { directory: string; text: string }[],
): Promise<EnvPasteResult[]> {
  return apiFetch(`/forge-api/projects/${projectId}/env`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ services }),
  }).then((r) => parse<EnvPasteResult[]>(r));
}

export function deleteProject(id: string, confirm: string): Promise<void> {
  return apiFetch(`/forge-api/projects/${id}`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ confirm }),
  }).then((r) => {
    if (!r.ok && r.status !== 204) {
      return parse<void>(r);
    }
  });
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

export type DeployEnvironment = "production" | "preview";

function withEnvironment(path: string, environment: DeployEnvironment = "production"): string {
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}environment=${encodeURIComponent(environment)}`;
}

export function createApplicationDeployment(
  applicationId: string,
  environment: DeployEnvironment = "production",
): Promise<Deployment> {
  return apiFetch(`/forge-api/applications/${applicationId}/deployments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ environment }),
  }).then((r) => parse<Deployment>(r));
}

export function getDeployment(id: string): Promise<Deployment> {
  return apiFetch(`/forge-api/deployments/${id}`).then((r) => parse<Deployment>(r));
}

export type EnvTemplate = {
  path: string;
  directory: string;
  source?: string;
  vars: { key: string; value: string }[];
};

export function listEnvTemplates(applicationId: string): Promise<EnvTemplate[]> {
  return apiFetch(`/forge-api/applications/${applicationId}/env-templates`).then((r) => parse<EnvTemplate[]>(r));
}

export function applyEnvTemplate(
  applicationId: string,
  directory: string,
  vars: EnvVar[],
): Promise<{ application_id: string; name: string; root_directory: string }> {
  return apiFetch(`/forge-api/applications/${applicationId}/env-templates`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ directory, vars }),
  }).then((r) => parse<{ application_id: string; name: string; root_directory: string }>(r));
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

export function retryDeployment(id: string): Promise<Deployment> {
  return apiFetch(`/forge-api/deployments/${id}/retry`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({}),
  }).then((r) => parse<Deployment>(r));
}

export function rollbackDeployment(sourceId: string): Promise<Deployment> {
  return apiFetch(`/forge-api/deployments/${sourceId}/rollback`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({}),
  }).then((r) => parse<Deployment>(r));
}

export { mergeEnv, parseDotEnv, parseDotEnvDetailed } from "./dotenv";

export type ObserveService = {
  application_id: string;
  application_name: string;
  project_id: string;
  environment: string;
  health: string;
  health_reason: string;
  release_id?: string;
  release_status?: string;
  release_sha?: string;
  attempts: number;
  container: string;
  status_at?: string;
  failed_deployment_id?: string;
  failed_stage?: string;
  failed_reason?: string;
  restart_count: string;
  restart_count_reason: string;
  resources: string;
  resources_reason: string;
  uptime: string;
  uptime_reason: string;
};

export type ObserveMetrics = {
  http_requests_total: number;
  http_errors_total: number;
  deploys_queued_total: number;
  started_at?: string;
  collected_at?: string;
  counter_scope?: string;
  deployments_total: number;
  deployments_live: number;
  deployments_failed: number;
  deployments_in_progress?: number;
  applications_observed?: number;
  applications_unique?: number;
  last_deploy_duration_ms: number;
  containers_running: number;
  containers_checked: number;
  nodes_ready: number;
  nodes_dead: number;
  queue_depth?: string;
  queue_depth_reason?: string;
  services?: ObserveService[];
};

export type WorkerNode = {
  id: string;
  name: string;
  status: string;
  advertise_host?: string;
  join_token?: string;
  in_progress: number;
  last_seen_at?: string;
  created_at: string;
  updated_at: string;
};

export type ServiceLink = {
  id: string;
  source_application_id: string;
  target_application_id: string;
  target_name?: string;
  env_key: string;
  url?: string;
  created_at: string;
};

export function listLinks(applicationId: string): Promise<ServiceLink[]> {
  return apiFetch(`/forge-api/applications/${applicationId}/links`).then((r) => parse<ServiceLink[]>(r));
}

export function createLink(
  applicationId: string,
  targetApplicationId: string,
  envKey: string,
): Promise<ServiceLink> {
  return apiFetch(`/forge-api/applications/${applicationId}/links`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ target_application_id: targetApplicationId, env_key: envKey }),
  }).then((r) => parse<ServiceLink>(r));
}

export type AppVolume = {
  id: string;
  application_id: string;
  name: string;
  mount_path: string;
  docker_name: string;
  preview_docker_name?: string;
};

export function listVolumes(applicationId: string): Promise<AppVolume[]> {
  return apiFetch(`/forge-api/applications/${applicationId}/volumes`).then((r) => parse<AppVolume[]>(r));
}

export function createVolume(applicationId: string, name: string, mountPath: string): Promise<AppVolume> {
  return apiFetch(`/forge-api/applications/${applicationId}/volumes`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, mount_path: mountPath }),
  }).then((r) => parse<AppVolume>(r));
}

export function deleteVolume(applicationId: string, volumeId: string): Promise<void> {
  return apiFetch(`/forge-api/applications/${applicationId}/volumes/${volumeId}`, { method: "DELETE" }).then(
    (r) => {
      if (!r.ok && r.status !== 204) {
        return parse<void>(r);
      }
    },
  );
}

export type AppDomain = {
  id: string;
  application_id: string;
  hostname: string;
  environment: string;
  status: string;
  origin?: string;
  txt_name: string;
  txt_value: string;
};

export function listDomains(applicationId: string): Promise<AppDomain[]> {
  return apiFetch(`/forge-api/applications/${applicationId}/domains`).then((r) => parse<AppDomain[]>(r));
}

export function createDomain(applicationId: string, hostname: string, environment: string): Promise<AppDomain> {
  return apiFetch(`/forge-api/applications/${applicationId}/domains`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ hostname, environment }),
  }).then((r) => parse<AppDomain>(r));
}

export function verifyDomain(applicationId: string, domainId: string): Promise<AppDomain> {
  return apiFetch(`/forge-api/applications/${applicationId}/domains/${domainId}/verify`, { method: "POST" }).then(
    (r) => parse<AppDomain>(r),
  );
}

export type GitHubStatus = {
  configured: boolean;
  connected: boolean;
  login?: string;
};

export type GitHubRepo = {
  full_name: string;
  private: boolean;
  default_branch: string;
};

export type ApplicationGitHub = {
  connected: boolean;
  repo?: string;
  branch?: string;
  environment?: string;
  webhook?: boolean;
  warning?: string;
};

export function getGitHubStatus(): Promise<GitHubStatus> {
  return apiFetch("/forge-api/integrations/github/status").then((r) => parse<GitHubStatus>(r));
}

export function listGitHubRepos(): Promise<GitHubRepo[]> {
  return apiFetch("/forge-api/integrations/github/repos").then((r) => parse<GitHubRepo[]>(r));
}

export function getApplicationGitHub(applicationId: string): Promise<ApplicationGitHub> {
  return apiFetch(`/forge-api/applications/${applicationId}/github`).then((r) => parse<ApplicationGitHub>(r));
}

export function connectApplicationGitHub(
  applicationId: string,
  repo: string,
  branch: string,
  environment: string,
): Promise<ApplicationGitHub> {
  return apiFetch(`/forge-api/applications/${applicationId}/github`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ repo, branch, environment }),
  }).then((r) => parse<ApplicationGitHub>(r));
}

export function disconnectApplicationGitHub(applicationId: string): Promise<void> {
  return apiFetch(`/forge-api/applications/${applicationId}/github`, { method: "DELETE" }).then((r) => {
    if (!r.ok && r.status !== 204) {
      return parse<void>(r);
    }
  });
}

export function disconnectGitHub(): Promise<void> {
  return apiFetch("/forge-api/integrations/github/disconnect", { method: "POST" }).then((r) => {
    if (!r.ok && r.status !== 204) {
      return parse<void>(r);
    }
  });
}

export function deleteDomain(applicationId: string, domainId: string): Promise<void> {
  return apiFetch(`/forge-api/applications/${applicationId}/domains/${domainId}`, { method: "DELETE" }).then(
    (r) => {
      if (!r.ok && r.status !== 204) {
        return parse<void>(r);
      }
    },
  );
}

export function deleteLink(applicationId: string, linkId: string): Promise<void> {
  return apiFetch(`/forge-api/applications/${applicationId}/links/${linkId}`, { method: "DELETE" }).then(
    (r) => {
      if (!r.ok && r.status !== 204) {
        return parse<void>(r);
      }
    },
  );
}

export function getMetrics(): Promise<ObserveMetrics> {
  return apiFetch("/forge-api/metrics").then((r) => parse<ObserveMetrics>(r));
}

export function listNodes(): Promise<WorkerNode[]> {
  return apiFetch("/forge-api/nodes").then((r) => parse<WorkerNode[]>(r));
}

export function createNode(name: string, advertiseHost = ""): Promise<WorkerNode> {
  return apiFetch("/forge-api/nodes", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, advertise_host: advertiseHost }),
  }).then((r) => parse<WorkerNode>(r));
}

export function getApplicationLogs(
  applicationId: string,
  tail = 100,
  environment: DeployEnvironment = "production",
): Promise<AppLogs> {
  return apiFetch(
    withEnvironment(`/forge-api/applications/${applicationId}/logs?tail=${tail}`, environment),
  ).then((r) => parse<AppLogs>(r));
}

export function getApplicationHealth(
  applicationId: string,
  environment: DeployEnvironment = "production",
): Promise<AppHealth> {
  return apiFetch(withEnvironment(`/forge-api/applications/${applicationId}/health`, environment)).then(
    (r) => parse<AppHealth>(r),
  );
}

export function stopApplication(
  applicationId: string,
  environment: DeployEnvironment = "production",
): Promise<Deployment> {
  return apiFetch(`/forge-api/applications/${applicationId}/stop`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ environment }),
  }).then((r) => parse<Deployment>(r));
}

export function restartApplication(
  applicationId: string,
  environment: DeployEnvironment = "production",
): Promise<Deployment> {
  return apiFetch(`/forge-api/applications/${applicationId}/restart`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ environment }),
  }).then((r) => parse<Deployment>(r));
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
