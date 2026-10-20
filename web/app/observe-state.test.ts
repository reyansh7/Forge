import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import {
  OBSERVE_STACK_MAX_PX,
  containerPhrase,
  counterWindow,
  healthLabel,
  nextAction,
  observeView,
  platformSummary,
} from "./observe-state.ts";

test("loading, error, and stale views stay distinct", () => {
  assert.equal(observeView(false, false), "loading");
  assert.equal(observeView(false, true), "error");
  assert.equal(observeView(true, true), "stale");
  assert.equal(observeView(true, false), "ready");
});

test("missing probe data is not labeled stopped", () => {
  assert.equal(containerPhrase("not_probed"), "Not probed");
  assert.equal(containerPhrase("probe_error"), "Probe failed");
  assert.notEqual(healthLabel("unknown"), "Stopped");
  assert.equal(healthLabel("unhealthy"), "Unhealthy");
  assert.match(nextAction({ health: "unknown", container: "not_probed", release_status: "live" }), /not treat this as stopped/i);
  assert.match(nextAction({ health: "unhealthy", container: "not_probed", release_status: "failed" }), /open the deployment/i);
  assert.doesNotMatch(nextAction({ health: "unhealthy", container: "not_running", release_status: "live" }), /not treat this as stopped/i);
  assert.match(platformSummary(["unknown"]).detail, /not the same as stopped/i);
});

test("health words and platform rollup use only reported states", () => {
  assert.equal(healthLabel("healthy"), "Healthy");
  assert.equal(healthLabel("degraded"), "Degraded");
  assert.equal(healthLabel("stale"), "Stale");
  assert.equal(healthLabel("surprise"), "Unknown");
  assert.equal(platformSummary([]).label, "No applications");
  assert.equal(platformSummary(["healthy", "healthy"]).tone, "healthy");
  assert.equal(platformSummary(["healthy", "unhealthy"]).tone, "unhealthy");
  assert.equal(platformSummary(["degraded"]).tone, "degraded");
  assert.match(counterWindow(undefined), /not a historical total|do not read these counters as historical/i);
  assert.match(counterWindow("2026-10-19T00:00:00Z"), /not a historical total/i);
});

test("observe layout stacks on a narrow viewport", () => {
  const css = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "globals.css"), "utf8");
  assert.match(css, new RegExp(`@media \\(max-width: ${OBSERVE_STACK_MAX_PX}px\\)`));
  assert.match(css, /\.observe-layout/);
  assert.match(css, /\.observe-services/);
  assert.match(css, /prefers-reduced-motion:\s*reduce/);
});
