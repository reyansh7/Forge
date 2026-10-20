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

export type EnvPreview = {
  key: string;
  masked: string;
  kind: "build" | "runtime";
  placeholder: boolean;
};

/**
 * Parse a pasted .env file.
 *
 * Comments and blank lines are ignored. Quoted values may contain "=".
 * The last duplicate key wins. Reserved keys are skipped. Messages name
 * the line and, for a valid key, the key. They do not include values.
 */
export function parseDotEnvDetailed(text: string): DotEnvParse {
  const byKey = new Map<string, string>();
  const skipped: string[] = [];
  const raw = text.replace(/^\uFEFF/, "");
  raw.split(/\r?\n/).forEach((original, index) => {
    const lineNo = index + 1;
    let line = original.trim();
    if (!line || line.startsWith("#")) {
      return;
    }
    if (line.startsWith("export ")) {
      line = line.slice(7).trim();
    }
    const eq = line.indexOf("=");
    if (eq < 1) {
      skipped.push(`line ${lineNo} is not KEY=VALUE`);
      return;
    }
    const key = line.slice(0, eq).trim();
    const reserved = reservedEnvReason(key);
    if (reserved) {
      skipped.push(`line ${lineNo}: ${reserved}`);
      return;
    }
    if (!envKeyPattern.test(key)) {
      skipped.push(`line ${lineNo} key is not a shell identifier`);
      return;
    }
    const value = unquote(line.slice(eq + 1).trim());
    if (value.includes("\n") || value.includes("\r")) {
      skipped.push(`line ${lineNo} value is not a single line`);
      return;
    }
    if (byKey.has(key)) {
      skipped.push(`${key} was set more than once; the last value is kept`);
    }
    byKey.set(key, value);
  });
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

export function envVarClass(key: string): "build" | "runtime" {
  if (key.startsWith("NEXT_PUBLIC_") || key.startsWith("VITE_") || key.startsWith("REACT_APP_")) {
    return "build";
  }
  return "runtime";
}

/** Mask for the preview. Never returns the typed value. */
export function maskEnvValue(value: string): string {
  return value === "" ? "(empty)" : "••••••••";
}

export function placeholderWarning(key: string, value: string): boolean {
  const v = value.trim().toLowerCase();
  if (!v) {
    return false;
  }
  const obvious = new Set([
    "password",
    "secret",
    "changeme",
    "change-me",
    "change_me",
    "your_password",
    "your-password",
    "placeholder",
    "todo",
    "example",
    "xxx",
    "xxxx",
    "admin",
    "root",
    "test",
    "123456",
    "password123",
    "postgres",
    "postgres_secure_dev_password",
  ]);
  return (
    obvious.has(v) ||
    v.includes("changeme") ||
    v.includes("placeholder") ||
    v.includes("your-password") ||
    v.includes("your_password") ||
    v.includes("replace-me") ||
    v.includes("replace_me")
  );
}

export function previewDotEnv(text: string): { parsed: DotEnvParse; rows: EnvPreview[] } {
  const parsed = parseDotEnvDetailed(text);
  return {
    parsed,
    rows: parsed.vars.map((ev) => ({
      key: ev.key,
      masked: maskEnvValue(ev.value),
      kind: envVarClass(ev.key),
      placeholder: placeholderWarning(ev.key, ev.value),
    })),
  };
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
  const trimmed = value.trim();
  if (trimmed.length >= 2 && trimmed.startsWith("'") && trimmed.endsWith("'")) {
    return trimmed.slice(1, -1);
  }
  if (trimmed.length >= 2 && trimmed.startsWith('"') && trimmed.endsWith('"')) {
    return trimmed
      .slice(1, -1)
      .replace(/\\n/g, "\n")
      .replace(/\\r/g, "\r")
      .replace(/\\t/g, "\t")
      .replace(/\\(.)/g, "$1");
  }
  return stripInlineComment(trimmed);
}
