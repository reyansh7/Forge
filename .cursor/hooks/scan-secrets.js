#!/usr/bin/env node
"use strict";

/**
 * afterFileEdit: warn if an edited file looks like it contains secrets.
 * Does not format code or run tests.
 */

const fs = require("fs");
const path = require("path");

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

const PATTERNS = [
  { re: /-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/, name: "private key block" },
  { re: /\bAKIA[0-9A-Z]{16}\b/, name: "AWS access key id" },
  { re: /\bghp_[A-Za-z0-9]{20,}\b/, name: "GitHub token" },
  { re: /\bgithub_pat_[A-Za-z0-9_]{20,}\b/, name: "GitHub fine-grained token" },
  { re: /\bsk-(?:live|test)-[A-Za-z0-9]{20,}\b/, name: "secret API key" },
  { re: /\bxox[baprs]-[A-Za-z0-9-]{10,}\b/, name: "Slack token" },
];

function fileText(input) {
  const parts = [];
  if (typeof input.content === "string") {
    parts.push(input.content);
  }
  if (Array.isArray(input.edits)) {
    for (const edit of input.edits) {
      if (edit && typeof edit.new_string === "string") {
        parts.push(edit.new_string);
      }
      if (edit && typeof edit.content === "string") {
        parts.push(edit.content);
      }
    }
  }
  const filePath = input.file_path || input.path || input.file;
  if (filePath && fs.existsSync(filePath) && fs.statSync(filePath).isFile()) {
    try {
      parts.push(fs.readFileSync(filePath, "utf8"));
    } catch {
      // ignore unreadable files
    }
  }
  return parts.join("\n");
}

function main() {
  let input = {};
  const raw = readStdin().trim();
  if (raw) {
    try {
      input = JSON.parse(raw);
    } catch {
      reply({});
      return;
    }
  }

  const filePath = String(input.file_path || input.path || input.file || "");
  const base = path.basename(filePath).toLowerCase();
  if (base === ".env.example") {
    reply({});
    return;
  }

  const text = fileText(input);
  const hits = [];
  for (const pattern of PATTERNS) {
    if (pattern.re.test(text)) {
      hits.push(pattern.name);
    }
  }

  if (hits.length === 0) {
    reply({});
    return;
  }

  const names = hits.join(", ");
  reply({
    additional_context:
      "Secret scan: " +
      (filePath || "edited file") +
      " looks like it contains " +
      names +
      ". Do not commit this. Redact the secret, rotate it if it was real, and never print credentials.",
  });
}

main();
