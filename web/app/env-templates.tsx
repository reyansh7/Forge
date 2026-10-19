"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { applyEnvTemplate, type EnvTemplate } from "./api";

// One form per service folder.
//
// The parent loads the repository files before this card paints, so the
// operator does not see an empty form and then the file a moment later.
// templates === null means that read is still in flight. A folder other
// than the application already open is saved as its own service.
export function EnvTemplates({
  applicationId,
  templates,
  onSaved,
}: {
  applicationId: string;
  templates: EnvTemplate[] | null;
  onSaved?: () => void;
}) {
  const [values, setValues] = useState<Record<string, Record<string, string>>>({});
  const [note, setNote] = useState("");
  const [savedId, setSavedId] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");

  useEffect(() => {
    if (!templates) {
      return;
    }
    const next: Record<string, Record<string, string>> = {};
    for (const row of templates) {
      next[row.directory] = {};
      for (const item of row.vars ?? []) {
        next[row.directory][item.key] = item.value ?? "";
      }
    }
    setValues(next);
  }, [templates]);

  if (templates === null) {
    return <p className="empty">Reading environment files from the repository…</p>;
  }
  if (templates.length === 0) {
    return null;
  }
  const rows = templates;

  async function save(row: EnvTemplate) {
    setBusy(row.path);
    setErr("");
    setNote("");
    const filled = (row.vars ?? [])
      .map((item) => ({ key: item.key, value: values[row.directory]?.[item.key] ?? item.value ?? "" }))
      .filter((item) => item.value.trim() !== "");
    try {
      const saved = await applyEnvTemplate(applicationId, row.directory, filled);
      setSavedId(saved.application_id === applicationId ? "" : saved.application_id);
      onSaved?.();
      if (saved.application_id === applicationId) {
        setNote(`Saved ${filled.length} values on this application. Deploy again so the build can see them.`);
      } else {
        setNote(`Saved on ${saved.name}. That folder keeps its own environment. Deploy that service to build ${saved.root_directory}.`);
      }
    } catch (e) {
      setErr(e instanceof Error ? e.message : "could not save environment");
    } finally {
      setBusy("");
    }
  }

  return (
    <div style={{ marginTop: "1rem" }}>
      <p className="hint" style={{ marginTop: 0 }}>
        {rows.length > 1
          ? "Each folder has its own environment. Values are already filled from the repository. Save a folder to store those keys on that service."
          : `Values are already filled from ${rows[0].source || rows[0].path}. Save them to use them on the next deploy.`}
      </p>
      {rows.map((row) => (
        <form
          key={row.directory}
          className="card"
          style={{ marginTop: "0.75rem", padding: "0.9rem" }}
          onSubmit={(e) => {
            e.preventDefault();
            void save(row);
          }}
        >
          <h2 style={{ fontSize: "1rem" }}>{row.directory === "." ? "Repository root" : row.directory}</h2>
          <p className="empty">{row.source ? `From ${row.source}` : row.path}</p>
          {(row.vars ?? []).length === 0 ? (
            <p className="empty">No keys in this example file.</p>
          ) : (
            row.vars.map((item) => (
              <div key={item.key} className="form-grid" style={{ marginTop: "0.45rem" }}>
                <label htmlFor={`${row.path}-${item.key}`}>{item.key}</label>
                <input
                  id={`${row.directory}-${item.key}`}
                  type="text"
                  autoComplete="off"
                  value={values[row.directory]?.[item.key] ?? item.value ?? ""}
                  onChange={(e) =>
                    setValues((cur) => ({
                      ...cur,
                      [row.directory]: { ...cur[row.directory], [item.key]: e.target.value },
                    }))
                  }
                />
              </div>
            ))
          )}
          <div className="actions">
            <button className="btn-primary" type="submit" disabled={busy === row.path}>
              {busy === row.path ? "Saving…" : "Save these values"}
            </button>
          </div>
        </form>
      ))}
      {note ? (
        <p className="hint">
          {note}{" "}
          {savedId ? <Link href={`/applications/${savedId}`}>Open it</Link> : null}
        </p>
      ) : null}
      {err ? <p className="error">{err}</p> : null}
    </div>
  );
}
