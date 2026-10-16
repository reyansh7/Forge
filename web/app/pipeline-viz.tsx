"use client";

// Login hero illustration of Forge's real deploy loop:
// push → detect → build → deploy → live, with matching log lines.
//
// One looping GSAP timeline lights each stage, fills the connector to
// the next one, and reveals that stage's log line, then fades back and
// repeats. Purely decorative (aria-hidden); the labels mirror the actual
// worker stages so the picture is not marketing fiction.

import { useRef } from "react";
import gsap from "gsap";
import { useGSAP } from "@gsap/react";
import { whenSplashDone } from "./splash";

gsap.registerPlugin(useGSAP);

const STAGES = [
  { label: "push", icon: "M6 3v12M6 15a3 3 0 1 0 0 6 3 3 0 0 0 0-6ZM18 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6ZM18 9a9 9 0 0 1-9 9" },
  { label: "detect", icon: "M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14ZM21 21l-4.3-4.3" },
  { label: "build", icon: "M21 8 12 3 3 8v8l9 5 9-5V8ZM3 8l9 5 9-5M12 13v8" },
  { label: "deploy", icon: "M5 12h14M13 6l6 6-6 6" },
  { label: "live", icon: "M20 6 9 17l-5-5" },
];

const LINES = [
  <>
    <span className="p">$</span> git push origin main
  </>,
  <>
    <span className="p">→</span> detected node · next.js
  </>,
  <>
    <span className="p">→</span> built image forge/app:3f2a9c
  </>,
  <>
    <span className="p">→</span> health check passed · cutover
  </>,
  <>
    <span className="ok">✓</span> live at app.localhost
  </>,
];

export function PipelineViz() {
  const root = useRef<HTMLDivElement>(null);

  useGSAP(
    () => {
      const scope = root.current;
      const glows = gsap.utils.toArray<HTMLElement>(".pv-glow", scope);
      const icons = gsap.utils.toArray<HTMLElement>(".pv-core svg", scope);
      const fills = gsap.utils.toArray<HTMLElement>(".pv-link-fill", scope);
      const lines = gsap.utils.toArray<HTMLElement>(".pv-line", scope);

      if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
        gsap.set(glows, { opacity: 1 });
        gsap.set(icons, { color: "#fff" });
        gsap.set(fills, { scaleX: 1 });
        gsap.set(lines, { opacity: 1 });
        return;
      }

      const tl = gsap.timeline({ repeat: -1, repeatDelay: 0.6, paused: true });
      STAGES.forEach((_, i) => {
        tl.to(glows[i], { opacity: 1, duration: 0.35, ease: "power2.out" })
          .to(icons[i], { color: "#fff", duration: 0.25 }, "<")
          .fromTo(glows[i], { scale: 0.7 }, { scale: 1, duration: 0.5, ease: "back.out(3)" }, "<")
          .fromTo(lines[i], { opacity: 0, x: -8 }, { opacity: 1, x: 0, duration: 0.35, ease: "power2.out" }, "<");
        if (fills[i]) tl.to(fills[i], { scaleX: 1, duration: 0.55, ease: "power2.inOut" }, ">-0.05");
      });
      tl.to({}, { duration: 1.6 })
        .to([...glows, ...lines], { opacity: 0, duration: 0.5, ease: "power2.in" })
        .to(icons, { color: "#8a8a96", duration: 0.4 }, "<")
        .to(fills, { scaleX: 0, transformOrigin: "100% 50%", duration: 0.5, ease: "power2.in" }, "<")
        .set(fills, { transformOrigin: "0% 50%" });

      return whenSplashDone(() => tl.delay(0.9).play());
    },
    { scope: root },
  );

  return (
    <div ref={root} className="pv" aria-hidden="true">
      <div className="pv-track">
        {STAGES.map((s, i) => (
          <div key={s.label} style={{ display: "contents" }}>
            <div className={`pv-node${s.label === "live" ? " is-live" : ""}`}>
              <div className="pv-core">
                <span className="pv-glow" />
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d={s.icon} />
                </svg>
              </div>
              <span className="pv-label">{s.label}</span>
            </div>
            {i < STAGES.length - 1 ? (
              <div className="pv-link">
                <div className="pv-link-fill" />
              </div>
            ) : null}
          </div>
        ))}
      </div>
      <div className="pv-term">
        {LINES.map((l, i) => (
          <div key={i} className="pv-line">
            {l}
          </div>
        ))}
      </div>
    </div>
  );
}
