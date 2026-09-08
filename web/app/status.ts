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

export function isInProgress(status: string): boolean {
  return status !== "live" && status !== "failed" && status !== "stopped";
}
