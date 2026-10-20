"use client";

import { FormEvent, useState } from "react";
import { previewDotEnv } from "./dotenv";
import { saveProjectEnv, type EnvPasteResult } from "./api";

export type EnvServiceSlot = {
  name: string;
  directory: string;
  path: string;
};

export function envServiceSlots(
  plan: { name: string; root: string; deployable?: boolean }[] | null,
  apps: { name: string; root_directory?: string }[],
): EnvServiceSlot[] {
  const slots: EnvServiceSlot[] = [];
  const seen = new Set<string>();
  const add = (name: string, directory: string) => {
    const dir = directory.trim() || ".";
    if (seen.has(dir) || name === "preflight") {
      return;
    }
    seen.add(dir);
    slots.push({
      name: name || (dir === "." ? "app" : dir),
      directory: dir,
      path: dir === "." ? ".env" : `${dir}/.env`,
    });
  };
  for (const app of apps) {
    add(app.name, app.root_directory || ".");
  }
  for (const service of plan ?? []) {
    add(service.name, service.root || ".");
  }
  return slots;
}

// One empty paste area per service.
//
// The text starts empty on purpose. Example files in the repository are
// not copied here. Saving merges the pasted keys into that service and
// leaves every other stored key in place.
export function ServiceEnvEditors({
  projectId,
  slots,
  onSaved,
}: {
  projectId: string;
  slots: EnvServiceSlot[];
  onSaved?: () => void;
}) {
  const [text, setText] = useState<Record<string, string>>({});
  const [note, setNote] = useState<EnvPasteResult[] | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const services = slots
      .map((slot) => ({ directory: slot.directory, text: text[slot.directory] ?? "" }))
      .filter((slot) => slot.text.trim() !== "");
    if (services.length === 0) {
      setErr("Paste at least one KEY=VALUE line. An empty box does not change stored variables.");
      return;
    }
    setBusy(true);
    setErr("");
    setNote(null);
    try {
      const saved = await saveProjectEnv(projectId, services);
      setNote(saved);
      setText({});
      onSaved?.();
    } catch (error) {
      setErr(error instanceof Error ? error.message : "could not save environment");
    } finally {
      setBusy(false);
    }
  }

  if (slots.length === 0) {
    return <p className="empty">No services detected yet.</p>;
  }

  return (
    <form className="env-import" onSubmit={(e) => void onSubmit(e)}>
      <p className="hint" style={{ marginTop: 0 }}>
        Each service has its own environment. These boxes start empty. A repository example file is not copied in.
        Saving writes only the keys in that box and leaves other stored keys unchanged. Remove a stored key from the list below.
      </p>
      {slots.map((slot) => {
        const value = text[slot.directory] ?? "";
        const preview = value.trim() ? previewDotEnv(value) : null;
        return (
          <div className="card env-service" key={slot.directory}>
            <h2>{slot.name}</h2>
            <p className="empty">{slot.path}</p>
            <label htmlFor={`env-${slot.directory}`}>Paste .env</label>
            <textarea
              id={`env-${slot.directory}`}
              value={value}
              onChange={(e) => setText((cur) => ({ ...cur, [slot.directory]: e.target.value }))}
              placeholder={"KEY=value\n# comments are ignored"}
              aria-label={`${slot.name} ${slot.path}`}
              spellCheck={false}
              autoComplete="off"
            />
            {preview ? (
              <div className="env-preview">
                {preview.rows.length === 0 ? <p>No variables detected yet.</p> : null}
                {preview.rows.map((row) => (
                  <p key={row.key}>
                    <code>{row.key}</code> {row.masked} · {row.kind === "build" ? "build-time" : "runtime"}
                    {row.placeholder ? " · looks like a placeholder" : ""}
                  </p>
                ))}
                {preview.parsed.skipped.map((line, index) => (
                  <p key={`${index}-${line}`}>{line}</p>
                ))}
              </div>
            ) : null}
          </div>
        );
      })}
      <div className="actions">
        <button className="btn-primary" type="submit" disabled={busy}>
          {busy ? "Saving…" : "Save environments"}
        </button>
      </div>
      {note ? (
        <div className="env-preview">
          {note.map((row) => (
            <p key={row.directory}>
              {row.name || row.directory}: {row.saved.length} saved
              {row.kept.length > 0 ? `, kept ${row.kept.join(", ")}` : ""}
              {row.warnings.length > 0 ? `. ${row.warnings.join(". ")}` : ""}
              {row.issues.length > 0 ? `. ${row.issues.join(". ")}` : ""}
            </p>
          ))}
        </div>
      ) : null}
      {err ? <p className="error">{err}</p> : null}
    </form>
  );
}
