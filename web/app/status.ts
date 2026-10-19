/** Display labels for deployment rows. Values match the API status field. */
export const STAGES = [
  "queued",
  "detecting",
  "building",
  "provisioning",
  "deploying",
  "health_check",
  "live",
] as const;

const LABELS: Record<string, string> = {
  queued: "Queued",
  detecting: "Detecting",
  building: "Building",
  provisioning: "Provisioning",
  deploying: "Deploying",
  health_check: "Health Check",
  live: "Live",
  failed: "Failed",
  stopped: "Stopped",
};

export type DeploymentLike = {
  status: string;
  failed_stage?: string;
};

export function statusLabel(status: string): string {
  return LABELS[status] ?? status.replaceAll("_", " ");
}

export function pillClass(status: string): string {
  if (status === "live") return "pill live";
  if (status === "failed") return "pill failed";
  if (status === "stopped") return "pill stopped";
  return "pill pending";
}

export function stepClass(stage: string, d: DeploymentLike): string {
  if (d.status === "failed" && d.failed_stage === stage) return "step fail";
  if (d.status === "live") return "step done";
  const cur = STAGES.indexOf(d.status as (typeof STAGES)[number]);
  const here = STAGES.indexOf(stage as (typeof STAGES)[number]);
  if (here < 0) return "step";
  if (here < cur) return "step done";
  if (here === cur) return "step active";
  return "step";
}

export function when(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

export function formatDuration(ms?: number): string {
  if (ms == null || ms < 0 || Number.isNaN(ms)) {
    return "";
  }
  if (ms < 1000) {
    return `${Math.round(ms)} ms`;
  }
  const sec = ms / 1000;
  if (sec < 60) {
    return `${sec.toFixed(1)} s`;
  }
  const min = Math.floor(sec / 60);
  const rem = Math.round(sec % 60);
  return `${min}m ${rem}s`;
}

export function isInProgress(status: string): boolean {
  return status !== "live" && status !== "failed" && status !== "stopped";
}

export function elapsedSince(iso: string, now = Date.now()): string {
  const started = Date.parse(iso);
  if (Number.isNaN(started)) {
    return "";
  }
  return formatDuration(Math.max(0, now - started));
}

export type AttemptFacts = {
  status: string;
  failed_stage?: string;
  build_log?: string;
  updated_at?: string;
};

// stageExplanation describes the real pipeline step. It does not invent
// a percentage. A long detecting or building stage is still active.
export function stageExplanation(d: AttemptFacts): string {
  if (d.status === "failed") {
    const stage = d.failed_stage ? statusLabel(d.failed_stage) : "this attempt";
    if (!d.build_log && (d.failed_stage === "queued" || d.failed_stage === "detecting" || !d.failed_stage)) {
      return `Failed during ${stage}, before a Docker build started. There is no build log because the image was never built. The error below is from the platform.`;
    }
    if (d.build_log) {
      return `Failed during ${stage}. The text below is the builder output, not the running application's logs. A failed health check leaves the previous live release serving.`;
    }
    return `Failed during ${stage}. The error below is the platform record for this attempt.`;
  }
  switch (d.status) {
    case "queued":
      return "Waiting for a worker to claim this attempt. Nothing has been fetched or built, so there is no build log yet.";
    case "detecting":
      return "Fetching the repository and choosing a build. A large checkout can stay here for several minutes. Docker has not started, so a build log is not available yet.";
    case "building":
      return "Docker is building the image. Copying a large project can take many minutes before log lines are saved. The log appears when this stage finishes.";
    case "provisioning":
      return "Preparing the runtime for this attempt. Traffic has not moved.";
    case "deploying":
      return "Starting the new container. The previous live release keeps serving until the health check passes.";
    case "health_check":
      return "Waiting for the new container to answer its health check. If this never passes, the previous live release stays in place.";
    case "live":
      return "This attempt is the serving release for its environment.";
    case "stopped":
      return "An operator stopped this release. It is not receiving traffic.";
    default:
      return "";
  }
}

export function workerUnresponsiveNote(nodeStatus: string | undefined, nodeName: string | undefined): string | null {
  if (nodeStatus !== "dead") {
    return null;
  }
  const who = nodeName ? `Worker “${nodeName}”` : "The worker for this attempt";
  return `${who} is marked dead because it stopped sending heartbeats. That is separate from a slow build. Stop fails this attempt so you can deploy again. Stopping also takes the serving release in this environment offline. Volumes and images are kept.`;
}
