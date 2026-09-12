"use client";

import { FormEvent, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { login, setSessionToken, signup } from "../api";

type Panel = "signin" | "signup";

function panelFromHash(): Panel {
  if (typeof window === "undefined") {
    return "signin";
  }
  return window.location.hash === "#signup" ? "signup" : "signin";
}

export default function LoginPage() {
  const router = useRouter();
  const [panel, setPanel] = useState<Panel>("signin");
  const [signInName, setSignInName] = useState("");
  const [signInPassword, setSignInPassword] = useState("");
  const [signUpName, setSignUpName] = useState("");
  const [signUpPassword, setSignUpPassword] = useState("");
  const [signUpConfirm, setSignUpConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<"signin" | "signup" | "">("");

  useEffect(() => {
    const sync = () => setPanel(panelFromHash());
    sync();
    window.addEventListener("hashchange", sync);
    return () => window.removeEventListener("hashchange", sync);
  }, []);

  function showSignUp() {
    setError("");
    setPanel("signup");
    window.location.hash = "signup";
  }

  function showSignIn() {
    setError("");
    setPanel("signin");
    window.location.hash = "signin";
  }

  async function finish(sess: { token: string }) {
    setSessionToken(sess.token);
    router.replace("/");
  }

  async function onSignIn(e: FormEvent) {
    e.preventDefault();
    setBusy("signin");
    setError("");
    try {
      await finish(await login(signInName, signInPassword));
    } catch (err) {
      const msg = err instanceof Error ? err.message : "sign-in failed";
      if (msg === "invalid credentials") {
        setError("invalid-credentials");
      } else {
        setError(msg);
      }
    } finally {
      setBusy("");
    }
  }

  async function onSignUp(e: FormEvent) {
    e.preventDefault();
    setBusy("signup");
    setError("");
    if (signUpPassword !== signUpConfirm) {
      setError("passwords do not match");
      setBusy("");
      return;
    }
    try {
      await finish(await signup(signUpName, signUpPassword, signUpConfirm));
    } catch (err) {
      setError(err instanceof Error ? err.message : "sign-up failed");
    } finally {
      setBusy("");
    }
  }

  return (
    <>
      <section className="hero">
        <h1>{panel === "signup" ? "Create an account" : "Sign in"}</h1>
        <p className="lede">
          {panel === "signup"
            ? "Choose a name and password. This account only sees projects it creates. Passwords are stored as a bcrypt hash — not in git, not in logs."
            : "Sign in with your name and password. Each account has its own session cookie and only sees its own projects."}
        </p>
        {error === "invalid-credentials" ? (
          <p className="error">
            Invalid name or password. If you do not have an account yet,{" "}
            <a href="#signup" onClick={showSignUp}>
              create an account
            </a>
            .
          </p>
        ) : error ? (
          <p className="error">{error}</p>
        ) : null}
      </section>

      {panel === "signin" ? (
        <form className="card auth-card" onSubmit={onSignIn}>
          <h2>Sign in</h2>
          <label htmlFor="signin-name">Name</label>
          <input
            id="signin-name"
            name="username"
            autoComplete="username"
            value={signInName}
            onChange={(e) => setSignInName(e.target.value)}
            required
          />
          <label htmlFor="signin-password">Password</label>
          <input
            id="signin-password"
            name="password"
            type="password"
            autoComplete="current-password"
            value={signInPassword}
            onChange={(e) => setSignInPassword(e.target.value)}
            required
          />
          <div className="actions">
            <button className="btn-primary" type="submit" disabled={!!busy}>
              {busy === "signin" ? "Signing in…" : "Sign in"}
            </button>
          </div>
          <p className="hint">
            If you are not signed up,{" "}
            <a href="#signup" onClick={showSignUp}>
              create an account
            </a>
            .
          </p>
        </form>
      ) : (
        <form id="signup" className="card auth-card" onSubmit={onSignUp}>
          <h2>Sign up</h2>
          <label htmlFor="signup-name">Name</label>
          <input
            id="signup-name"
            name="new-username"
            autoComplete="username"
            value={signUpName}
            onChange={(e) => setSignUpName(e.target.value)}
            required
          />
          <label htmlFor="signup-password">Password</label>
          <input
            id="signup-password"
            name="new-password"
            type="password"
            autoComplete="new-password"
            value={signUpPassword}
            onChange={(e) => setSignUpPassword(e.target.value)}
            required
            minLength={10}
          />
          <label htmlFor="signup-confirm">Confirm password</label>
          <input
            id="signup-confirm"
            name="new-password-confirm"
            type="password"
            autoComplete="new-password"
            value={signUpConfirm}
            onChange={(e) => setSignUpConfirm(e.target.value)}
            required
            minLength={10}
          />
          <div className="actions">
            <button className="btn-primary" type="submit" disabled={!!busy}>
              {busy === "signup" ? "Creating…" : "Create account"}
            </button>
          </div>
          <p className="hint">Password must be at least 10 characters.</p>
          <p className="hint">
            Already have an account?{" "}
            <a href="#signin" onClick={showSignIn}>
              Sign in
            </a>
            .
          </p>
        </form>
      )}
    </>
  );
}
