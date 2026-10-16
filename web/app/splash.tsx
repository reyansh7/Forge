"use client";

// Loading screen shown once per browser session.
//
// Why server-rendered: the splash markup ships in the first HTML so the
// dashboard never flashes underneath it before hydration. A tiny inline
// script in layout.tsx sets html[data-splash="seen"] when this session
// already watched it (or the OS asks for reduced motion); CSS then hides
// it before first paint, and this component unmounts itself.
//
// Handoff: when the exit wipe starts, html[data-splash] becomes "leaving"
// and a "forge:splash-done" event fires. frame.tsx and SplitHeading wait
// for that so page entrance animations play as the curtain lifts, not
// hidden underneath it.

import { useRef, useState } from "react";
import gsap from "gsap";
import { useGSAP } from "@gsap/react";
import { F_PATH, HEX_PATH, LogoDefs } from "./logo";

gsap.registerPlugin(useGSAP);

const SPLASH_KEY = "forge:splash";
const SPARKS = 22;

/** Runs cb once the splash has started leaving (or immediately if none). */
export function whenSplashDone(cb: () => void): () => void {
  const state = document.documentElement.dataset.splash;
  if (state === "seen" || state === "leaving") {
    cb();
    return () => {};
  }
  const handler = () => cb();
  window.addEventListener("forge:splash-done", handler, { once: true });
  return () => window.removeEventListener("forge:splash-done", handler);
}

export function Splash() {
  const root = useRef<HTMLDivElement>(null);
  const pct = useRef<HTMLSpanElement>(null);
  const tl = useRef<gsap.core.Timeline | null>(null);
  const [gone, setGone] = useState(false);

  const { contextSafe } = useGSAP(
    () => {
      const el = root.current;
      const html = document.documentElement;
      if (!el || html.dataset.splash === "seen") {
        setGone(true);
        return;
      }

      const leave = () => {
        html.dataset.splash = "leaving";
        window.dispatchEvent(new Event("forge:splash-done"));
      };
      const finish = () => {
        try {
          sessionStorage.setItem(SPLASH_KEY, "1");
        } catch {
          // Private mode: the splash simply plays again next load.
        }
        html.dataset.splash = "seen";
        setGone(true);
      };

      const counter = { v: 0 };
      const t = gsap.timeline({ defaults: { ease: "power3.out" }, onComplete: finish });
      t.to(".splash-ring", { autoAlpha: 1, scale: 1, duration: 1, stagger: 0.12, ease: "expo.out" })
        .to(".splash-mark path.stroke", { strokeDashoffset: 0, duration: 1, stagger: 0.18, ease: "power2.inOut" }, 0.15)
        .to(".splash-mark .fill", { opacity: 1, duration: 0.5 }, "-=0.35")
        .to(".splash-letter", { y: 0, duration: 0.8, stagger: 0.07, ease: "expo.out" }, "-=0.55")
        .to(".splash-tag", { autoAlpha: 1, duration: 0.6 }, "-=0.45")
        .to(".splash-bar-fill", { scaleX: 1, duration: 1.15, ease: "power2.inOut" }, "-=0.7")
        .to(
          counter,
          {
            v: 100,
            duration: 1.15,
            ease: "power2.inOut",
            onUpdate: () => {
              if (pct.current) pct.current.textContent = String(Math.round(counter.v)).padStart(3, "0") + "%";
            },
          },
          "<",
        )
        .set(".splash-spark", { autoAlpha: 1 }, "-=0.15")
        .to(
          ".splash-spark",
          {
            x: () => gsap.utils.random(-260, 260),
            y: () => gsap.utils.random(-200, 200),
            scale: () => gsap.utils.random(0.4, 1.6),
            autoAlpha: 0,
            duration: 1.1,
            ease: "power3.out",
            stagger: 0.008,
          },
          "<",
        )
        .to(".splash-emblem", { scale: 1.08, duration: 0.25, yoyo: true, repeat: 1, ease: "power2.inOut" }, "<")
        .addLabel("exit", "-=0.55")
        .to(".splash-inner", { autoAlpha: 0, scale: 0.94, y: -20, duration: 0.5, ease: "power2.in" }, "exit")
        .to(el, { yPercent: -100, duration: 0.95, ease: "expo.inOut", onStart: leave }, "exit+=0.2");
      tl.current = t;
    },
    { scope: root },
  );

  const skip = contextSafe(() => {
    const t = tl.current;
    if (t && t.time() < (t.labels.exit ?? 0)) t.seek("exit");
  });

  if (gone) return null;

  return (
    <div ref={root} className="splash" onClick={skip} role="status" aria-label="Loading Forge">
      <div className="splash-inner">
        <div className="splash-emblem">
          <span className="splash-ring" />
          <span className="splash-ring two" />
          <svg className="splash-mark" viewBox="0 0 64 64" aria-hidden="true">
            <LogoDefs />
            <path className="fill" d={HEX_PATH} />
            <path className="stroke" d={HEX_PATH} pathLength={120} />
            <path className="stroke" d={F_PATH} pathLength={120} style={{ strokeWidth: 3.4 }} />
          </svg>
          {Array.from({ length: SPARKS }, (_, i) => (
            <span key={i} className="splash-spark" />
          ))}
        </div>
        <div className="splash-word" aria-hidden="true">
          {"FORGE".split("").map((c, i) => (
            <span key={i} className="splash-letter">
              {c}
            </span>
          ))}
        </div>
        <div className="splash-tag">Self-hosted platform</div>
        <div className="splash-bar">
          <div className="splash-bar-fill" />
        </div>
        <div className="splash-meta">
          <span>igniting control plane</span>
          <span ref={pct}>000%</span>
        </div>
      </div>
      <span className="splash-skip">click to skip</span>
    </div>
  );
}
