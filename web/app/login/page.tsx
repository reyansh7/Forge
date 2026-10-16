"use client";

import { FormEvent, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import gsap from "gsap";
import { useGSAP } from "@gsap/react";
import { login, setSessionToken, signup } from "../api";
import { PipelineViz } from "../pipeline-viz";
import { SplitHeading } from "../split";

gsap.registerPlugin(useGSAP);

type Panel = "signin" | "signup";

function panelFromHash(): Panel {
  if (typeof window === "undefined") {
    return "signin";
  }
  return window.location.hash === "#signup" ? "signup" : "signin";
}

function GitHubIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" fill="currentColor">
      <path d="M12 .5a11.5 11.5 0 0 0-3.64 22.41c.58.1.79-.25.79-.56v-2c-3.2.7-3.88-1.37-3.88-1.37-.53-1.33-1.28-1.69-1.28-1.69-1.05-.71.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.77 2.71 1.26 3.37.96.1-.75.4-1.26.73-1.55-2.55-.29-5.24-1.28-5.24-5.68 0-1.25.45-2.28 1.18-3.08-.12-.29-.51-1.46.11-3.04 0 0 .97-.31 3.17 1.18a11 11 0 0 1 5.77 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.58.23 2.75.11 3.04.74.8 1.18 1.83 1.18 3.08 0 4.41-2.69 5.39-5.25 5.67.41.36.78 1.06.78 2.14v3.17c0 .31.21.67.8.56A11.5 11.5 0 0 0 12 .5Z" />
    </svg>
  );
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
  const panelRef = useRef<HTMLDivElement>(null);
  const firstPanel = useRef(true);

  useEffect(() => {
    const sync = () => setPanel(panelFromHash());
    sync();
    const reason = new URLSearchParams(window.location.search).get("github");
    if (reason === "not_configured") {
      setError("GitHub sign-in is not configured. Set FORGE_GITHUB_CLIENT_ID and FORGE_GITHUB_CLIENT_SECRET in .env, then restart the API.");
    } else if (reason === "not_allowed") {
      setError("This GitHub account is not on the Forge allowlist. Add its numeric user id to FORGE_GITHUB_ALLOWED_USER_IDS and restart the API.");
    } else if (reason === "rate_limited") {
      setError("Too many GitHub sign-in attempts. Wait a few minutes and try again.");
    } else if (reason) {
      setError("GitHub sign-in did not finish. Try Continue with GitHub again.");
    }
    window.addEventListener("hashchange", sync);
    return () => window.removeEventListener("hashchange", sync);
  }, []);

  // Slide the segmented-control thumb and cross-fade the form whenever
  // the panel changes. The first run only positions the thumb so a
  // deep link to #signup does not animate on load.
  useGSAP(
    () => {
      const x = panel === "signup" ? "100%" : "0%";
      if (firstPanel.current) {
        firstPanel.current = false;
        gsap.set(".segmented-thumb", { x });
        return;
      }
      gsap.to(".segmented-thumb", { x, duration: 0.5, ease: "expo.out" });
      gsap.fromTo(
        ".auth-form > *",
        { autoAlpha: 0, y: 12 },
        { autoAlpha: 1, y: 0, duration: 0.45, ease: "power3.out", stagger: 0.04 },
      );
    },
    { scope: panelRef, dependencies: [panel] },
  );

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
    <div className="auth-stage">
      <section className="auth-hero">
        <span className="eyebrow">Self-hosted · Git-driven · Yours</span>
        <SplitHeading lines={[{ text: "Push code." }, { text: "Forge ships it.", gradient: true }]} delay={0.15} />
        <p className="lede">
          Connect a GitHub repository and Forge builds it in an isolated container, health-checks it,
          and routes traffic to it, all on your own machine.
        </p>
        <PipelineViz />
        <div className="feature-row">
          <span className="feature-chip">Zero-downtime cutover</span>
          <span className="feature-chip">Rollbacks</span>
          <span className="feature-chip">Live logs</span>
          <span className="feature-chip">Custom domains</span>
          <span className="feature-chip">Volumes</span>
        </div>
      </section>

      <div ref={panelRef} className="auth-panel">
        <h2>{panel === "signup" ? "Create your account" : "Welcome back"}</h2>
        <p className="sub">
          {panel === "signup"
            ? "Each account only sees projects it creates. Passwords are stored as a bcrypt hash."
            : "Sign in to your Forge control plane."}
        </p>

        <div className="segmented" role="group" aria-label="Account mode">
          <span className="segmented-thumb" aria-hidden="true" />
          <button type="button" aria-pressed={panel === "signin"} onClick={showSignIn}>
            Sign in
          </button>
          <button type="button" aria-pressed={panel === "signup"} onClick={showSignUp}>
            Create account
          </button>
        </div>

        <a className="btn btn-github" href="/forge-api/auth/github">
          <GitHubIcon />
          Continue with GitHub
        </a>
        <p className="hint" style={{ marginTop: "0.6rem" }}>
          A new GitHub user gets a Forge account. An existing password account is never taken over
          just because the names match.
        </p>

        <div className="divider">or with a password</div>

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

        {panel === "signin" ? (
          <form key="signin" className="auth-form" onSubmit={onSignIn}>
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
          </form>
        ) : (
          <form key="signup" id="signup" className="auth-form" onSubmit={onSignUp}>
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
            <p className="hint">At least 10 characters.</p>
            <div className="actions">
              <button className="btn-primary" type="submit" disabled={!!busy}>
                {busy === "signup" ? "Creating…" : "Create account"}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
