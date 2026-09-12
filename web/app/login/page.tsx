"use client";

import { FormEvent, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import {
  authStatus,
  bootstrap,
  login,
  setSessionToken,
} from "../api";

export default function LoginPage() {
  const router = useRouter();
  const [mode, setMode] = useState<"loading" | "bootstrap" | "login">("loading");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void authStatus()
      .then((s) => setMode(s.bootstrap_required ? "bootstrap" : "login"))
      .catch(() => {
        setError("Cannot reach the API");
        setMode("login");
      });
  }, []);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const sess = mode === "bootstrap" ? await bootstrap(username, password) : await login(username, password);
      setSessionToken(sess.token);
      router.replace("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "sign-in failed");
    } finally {
      setBusy(false);
    }
  }

  if (mode === "loading") {
    return (
      <section className="hero">
        <h1>Forge</h1>
        <p className="lede">Checking operator setup…</p>
      </section>
    );
  }

  return (
    <section className="hero" style={{ maxWidth: "28rem" }}>
      <h1>{mode === "bootstrap" ? "Create the first operator" : "Sign in"}</h1>
      <p className="lede">
        {mode === "bootstrap"
          ? "No operator exists yet. Username and password are stored as a bcrypt hash — not in git, not in logs."
          : "Control-plane APIs require a session. Loopback is still the bind; this is identity, not a public website."}
      </p>
      <form className="card" onSubmit={onSubmit}>
        <h2>{mode === "bootstrap" ? "Bootstrap" : "Operator"}</h2>
        <label htmlFor="username">Username</label>
        <input
          id="username"
          autoComplete="username"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          required
        />
        <label htmlFor="password">Password</label>
        <input
          id="password"
          type="password"
          autoComplete={mode === "bootstrap" ? "new-password" : "current-password"}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
          minLength={10}
        />
        {error ? <p className="error">{error}</p> : null}
        <div className="actions">
          <button className="btn-primary" type="submit" disabled={busy}>
            {busy ? "Working…" : mode === "bootstrap" ? "Create operator" : "Sign in"}
          </button>
        </div>
        <p className="hint">Password must be at least 10 characters. curl can use Authorization: Bearer instead of the dashboard.</p>
      </form>
    </section>
  );
}
