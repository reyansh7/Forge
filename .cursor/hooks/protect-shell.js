#!/usr/bin/env node
"use strict";

/**
 * beforeShellExecution: ask or deny destructive / secret-leaking commands.
 * Fail-open is configured in hooks.json (failClosed: false).
 */

const fs = require("fs");

function readStdin() {
  try {
    return fs.readFileSync(0, "utf8");
  } catch {
    return "";
  }
}

function reply(obj) {
  process.stdout.write(JSON.stringify(obj));
}

function main() {
  let input = {};
  const raw = readStdin().trim();
  if (raw) {
    try {
      input = JSON.parse(raw);
    } catch {
      reply({ permission: "allow" });
      return;
    }
  }

  const command = String(input.command || input.command_line || "");
  const c = command.replace(/\s+/g, " ").trim();
  if (!c) {
    reply({ permission: "allow" });
    return;
  }

  const deny = firstMatch(c, DENY);
  if (deny) {
    reply({
      permission: "deny",
      user_message: "Blocked: " + deny.reason,
      agent_message:
        "A Forge hook denied this shell command (" +
        deny.reason +
        "). Do not work around it. Ask the developer.",
    });
    return;
  }

  const ask = firstMatch(c, ASK);
  if (ask) {
    reply({
      permission: "ask",
      user_message: "Review this command: " + ask.reason,
      agent_message:
        "A Forge hook requires approval (" + ask.reason + "). Wait for the developer.",
    });
    return;
  }

  reply({ permission: "allow" });
}

function firstMatch(command, rules) {
  for (const rule of rules) {
    if (rule.re.test(command)) {
      return rule;
    }
  }
  return null;
}

const DENY = [
  {
    re: /\b(cat|type|Get-Content|gc)\b.*(\.env\b|credentials\.json|id_rsa|\.pem\b)/i,
    reason: "credential file dump",
  },
  {
    re: /\bprintenv\b|\benv\s*\|\s*(grep|Select-String)/i,
    reason: "environment credential dump",
  },
];

const ASK = [
  {
    re: /\bgit\b.*\bpush\b.*(--force|--force-with-lease|-f)\b/i,
    reason: "force push",
  },
  {
    re: /\bgit\b.*\breset\b.*--hard\b/i,
    reason: "git reset --hard",
  },
  {
    re: /\bgit\b.*\bclean\b.*-[a-zA-Z]*f/i,
    reason: "git clean -f",
  },
  {
    re: /\bgit\b\s+init\b/i,
    reason: "git init (not authorized in this environment setup)",
  },
  {
    re: /\b(rm\s+-[a-zA-Z]*r[a-zA-Z]*f|rmdir\s+\/s|del\s+\/s|Remove-Item\b.*(-Recurse|-r)\b)/i,
    reason: "recursive delete",
  },
  {
    re: /\b(terraform\s+destroy|pulumi\s+destroy|cdk\s+destroy)\b/i,
    reason: "infrastructure destroy",
  },
  {
    re: /\b(kubectl\s+delete|helm\s+uninstall)\b/i,
    reason: "cluster delete",
  },
  {
    re: /\b(aws|az|gcloud|wrangler)\b.*\b(delete|remove|destroy|terminate)\b/i,
    reason: "cloud destructive operation",
  },
  {
    re: /\bdocker\b.*\b(system\s+prune|volume\s+rm|rmi)\b/i,
    reason: "docker destructive prune/remove",
  },
  {
    re: /\b(drop\s+database|DROP\s+DATABASE|prisma\s+migrate\s+reset)\b/i,
    reason: "destructive database operation",
  },
];

main();
