export type EnvVar = {
  key: string;
  value: string;
};

/** Same identifier rule as store.ValidateEnvKey. */
const envKeyPattern = /^[A-Za-z_][A-Za-z0-9_]*$/;

export type DotEnvParse = {
  vars: EnvVar[];
  skipped: string[];
};

/**
 * Parse a pasted or uploaded .env file.
 *
 * Understands comments, blank lines, optional `export`, quotes, and
 * last-wins duplicates. Reserved Forge keys (PORT, HOST, FORGE_*) are
 * skipped with a reason so one bad line does not abort the rest.
 * Values are not logged by this function.
 */
export function parseDotEnvDetailed(text: string): DotEnvParse {
  const byKey = new Map<string, string>();
  const skipped: string[] = [];
  const raw = text.replace(/^\uFEFF/, "");
  for (const original of raw.split(/\r?\n/)) {
    let line = original.trim();
    if (!line || line.startsWith("#")) {
      continue;
    }
    if (line.startsWith("export ")) {
      line = line.slice(7).trim();
    }
    const eq = line.indexOf("=");
    if (eq < 1) {
      skipped.push(`ignored line without KEY=VALUE`);
      continue;
    }
    const key = line.slice(0, eq).trim();
    if (!envKeyPattern.test(key)) {
      skipped.push(`${key || "(empty)"} is not a valid env key`);
      continue;
    }
    const reserved = reservedEnvReason(key);
    if (reserved) {
      skipped.push(reserved);
      continue;
    }
    byKey.set(key, unquote(stripInlineComment(line.slice(eq + 1).trim())));
  }
  const vars = Array.from(byKey, ([key, value]) => ({ key, value }));
  vars.sort((a, b) => a.key.localeCompare(b.key));
  return { vars, skipped };
}

/** Split a pasted KEY=VALUE block. Lines starting with # are ignored. */
export function parseDotEnv(text: string): EnvVar[] {
  return parseDotEnvDetailed(text).vars;
}

export function mergeEnv(existing: EnvVar[], incoming: EnvVar[]): EnvVar[] {
  const byKey = new Map(existing.map((ev) => [ev.key, ev.value]));
  for (const ev of incoming) {
    byKey.set(ev.key, ev.value);
  }
  return Array.from(byKey, ([key, value]) => ({ key, value })).sort((a, b) =>
    a.key.localeCompare(b.key),
  );
}

export function reservedEnvReason(key: string): string {
  const upper = key.toUpperCase();
  if (upper === "PORT" || upper === "HOST") {
    return `${upper} is reserved by Forge`;
  }
  if (upper.startsWith("FORGE_")) {
    return `${key} is reserved by Forge`;
  }
  return "";
}

function stripInlineComment(value: string): string {
  if (value.startsWith('"') || value.startsWith("'")) {
    return value;
  }
  const hash = value.indexOf(" #");
  if (hash >= 0) {
    return value.slice(0, hash).trimEnd();
  }
  return value;
}

function unquote(value: string): string {
  if (value.length >= 2) {
    const a = value[0];
    const b = value[value.length - 1];
    if ((a === '"' && b === '"') || (a === "'" && b === "'")) {
      return value.slice(1, -1);
    }
  }
  return value;
}
