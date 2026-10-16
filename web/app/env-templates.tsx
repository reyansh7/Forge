"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { applyEnvTemplate, listEnvTemplates, type EnvTemplate } from "./api";

// One form per .env.example Forge found in the GitHub repository.
//
// A monorepo often has frontend/, backend/, and a model folder, each
// with its own example file. Saving a subfolder writes those values
// onto the application whose root directory is that folder, creating
// the application if this project does not have it yet. The root file
// stays on the application the operator already opened.
export function EnvTemplates({ applicationId }: { applicationId: string }) {
  const [rows, setRows] = useState<EnvTemplate[] | null>(null);
  const [values, setValues] = useState<Record<string, Record<string, string>>>({});
  const [note, setNote] = useState("");
  const [savedId, setSavedId] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");

  useEffect(() => {
    let cancelled = false;
    void listEnvTemplates(applicationId)
      .then((list) => {
        if (!cancelled) {
          setRows(list);
          const next: Record<string, Record<string, string>> = {};
          for (const row of list) {
            next[row.path] = {};
            for (const item of row.vars ?? []) {
              next[row.path][item.key] = item.value ?? "";
            }
          }
          setValues(next);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setRows([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [applicationId]);

  if (!rows || rows.length === 0) {
    return null;
  }

  async function save(row: EnvTemplate) {
    setBusy(row.path);
    setErr("");
    setNote("");
    const filled = (row.vars ?? [])
      .map((item) => ({ key: item.key, value: values[row.path]?.[item.key] ?? item.value ?? "" }))
      .filter((item) => item.value.trim() !== "");
    try {
      const saved = await applyEnvTemplate(applicationId, row.directory, filled);
      setSavedId(saved.application_id === applicationId ? "" : saved.application_id);
      if (saved.application_id === applicationId) {
        setNote(`Saved ${filled.length} values on this application. Deploy again so the build can see them.`);
      } else {
        setNote(`Saved on ${saved.name}. Deploy that service to build the ${saved.root_directory} folder.`);
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
        This repository has an environment file in more than one folder. Fill the keys for each folder.
        A folder other than the repository root is saved as its own service.
      </p>
      {rows.map((row) => (
        <form
          key={row.path}
          className="card"
          style={{ marginTop: "0.75rem", padding: "0.9rem" }}
          onSubmit={(e) => {
            e.preventDefault();
            void save(row);
          }}
        >
          <h2 style={{ fontSize: "1rem" }}>{row.directory === "." ? "Repository root" : row.directory}</h2>
          <p className="empty">{row.path}</p>
          {(row.vars ?? []).length === 0 ? (
            <p className="empty">No keys in this example file.</p>
          ) : (
            row.vars.map((item) => (
              <div key={item.key} className="form-grid" style={{ marginTop: "0.45rem" }}>
                <label htmlFor={`${row.path}-${item.key}`}>{item.key}</label>
                <input
                  id={`${row.path}-${item.key}`}
                  type="text"
                  autoComplete="off"
                  value={values[row.path]?.[item.key] ?? item.value ?? ""}
                  onChange={(e) =>
                    setValues((cur) => ({
                      ...cur,
                      [row.path]: { ...cur[row.path], [item.key]: e.target.value },
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
