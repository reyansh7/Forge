import assert from "node:assert/strict";
import test from "node:test";
import { pushDeployStatus } from "./github-binding-state.ts";

test("oauth and a git url are not push-to-deploy", () => {
  const linked = pushDeployStatus({ oauthConnected: true, bindingConnected: false });
  assert.equal(linked.enabled, false);
  assert.match(linked.detail, /save a repository and branch/i);
  const signedOut = pushDeployStatus({ oauthConnected: false, bindingConnected: false });
  assert.equal(signedOut.enabled, false);
  assert.match(signedOut.detail, /does not deploy pushes/i);
});

test("webhook flag and warning come from the API", () => {
  const off = pushDeployStatus({
    oauthConnected: true,
    bindingConnected: true,
    webhook: false,
    warning: "Saved. Press Deploy to build this commit. A git push deploys on its own only after Forge has a public address.",
  });
  assert.equal(off.enabled, false);
  assert.match(off.detail, /public address/);
  const on = pushDeployStatus({
    oauthConnected: true,
    bindingConnected: true,
    webhook: true,
    warning: "this warning must not override an active webhook",
  });
  assert.equal(on.enabled, true);
  assert.equal(on.label, "Push-to-deploy is on");
  assert.doesNotMatch(on.detail, /this warning/);
});
