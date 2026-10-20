/** Pure display rules for the Observe page.

The API decides health. These functions only choose the view, the
words, and the next action so a missing probe is never labeled stopped.
 */

export type ObserveView = "loading" | "error" | "ready" | "stale";

export const OBSERVE_STACK_MAX_PX = 800;

export function observeView(hasData: boolean, failed: boolean): ObserveView {
  if (!hasData && failed) {
    return "error";
  }
  if (!hasData) {
    return "loading";
  }
  if (failed) {
    return "stale";
  }
  return "ready";
}

export function healthLabel(health: string): string {
  switch (health) {
    case "healthy":
      return "Healthy";
    case "degraded":
      return "Degraded";
    case "unhealthy":
      return "Unhealthy";
    case "stale":
      return "Stale";
    default:
      return "Unknown";
  }
}

export function healthPill(health: string): string {
  switch (health) {
    case "healthy":
      return "pill live";
    case "unhealthy":
      return "pill failed";
    case "degraded":
    case "stale":
      return "pill pending";
    default:
      return "pill stopped";
  }
}

export function containerPhrase(container: string): string {
  switch (container) {
    case "running":
      return "Running";
    case "not_running":
      return "Not running";
    case "probe_error":
      return "Probe failed";
    case "not_probed":
      return "Not probed";
    default:
      return "Unavailable";
  }
}

export function counterWindow(startedAt?: string): string {
  if (startedAt) {
    return "This API process, since it started. Not a historical total.";
  }
  return "This API process. The response did not include when the process started, so do not read these counters as historical totals.";
}

export function platformSummary(states: string[]): { tone: string; label: string; detail: string } {
  if (states.length === 0) {
    return {
      tone: "unknown",
      label: "No applications",
      detail: "Nothing has been deployed yet. Create an application from a project to see health here.",
    };
  }
  const count = (name: string) => states.filter((state) => state === name).length;
  const unhealthy = count("unhealthy");
  const stale = count("stale");
  const degraded = count("degraded");
  const unknown = count("unknown");
  const healthy = count("healthy");
  if (unhealthy > 0) {
    return {
      tone: "unhealthy",
      label: "Needs attention",
      detail: `${unhealthy} release${unhealthy === 1 ? " needs" : "s need"} attention. Open the failed attempt or its logs.`,
    };
  }
  if (stale > 0) {
    return {
      tone: "stale",
      label: "Stale",
      detail: `${stale} worker heartbeat${stale === 1 ? " is" : "s are"} stale or a probe did not finish. A stale worker does not delete containers.`,
    };
  }
  if (degraded > 0) {
    return {
      tone: "degraded",
      label: "Degraded",
      detail: `${degraded} release${degraded === 1 ? " is" : "s are"} still serving, with a failed follow-up or a worker that stopped heartbeating.`,
    };
  }
  if (unknown > 0 && healthy === 0) {
    return {
      tone: "unknown",
      label: "Unknown",
      detail: "Forge does not have a probe result. Unknown is not the same as stopped.",
    };
  }
  if (unknown > 0) {
    return {
      tone: "degraded",
      label: "Partly observed",
      detail: `${healthy} healthy. ${unknown} without a probe result. The unprobed ones are not marked stopped.`,
    };
  }
  return {
    tone: "healthy",
    label: "Healthy",
    detail: "Every observed release that was probed is running.",
  };
}

export function nextAction(row: {
  health: string;
  container: string;
  release_status?: string;
}): string {
  if (row.health === "unhealthy" && row.release_status === "failed") {
    return "Open the deployment record and its logs. There is no live release for this environment.";
  }
  if (row.health === "unhealthy" && row.container === "not_running") {
    return "Open logs. Docker reported the container is not running. Deploy again after reading the error.";
  }
  if (row.container === "not_probed" || row.container === "probe_error") {
    return "Do not treat this as stopped. Refresh. A probe error or the 20-container cap leaves the state unknown.";
  }
  if (row.health === "stale" || row.health === "degraded") {
    return "Check the worker heartbeat. A stale worker does not delete the container. Stop and deploy again only if you need to move the workload.";
  }
  if (row.health === "healthy") {
    return "No action. The probed container is running.";
  }
  return "Open the application. Forge does not have enough probe data to call this stopped.";
}
