"use client";

// Headline whose characters rise into place.
//
// React renders the per-character spans itself (instead of splitting
// innerHTML after mount) so a re-render cannot fight GSAP over the DOM.
// Screen readers get the plain text through aria-label; the spans are
// aria-hidden. Each line is overflow:hidden, so characters start below
// the line box and appear to slide up out of a mask.

import { useRef } from "react";
import gsap from "gsap";
import { useGSAP } from "@gsap/react";
import { whenSplashDone } from "./splash";

gsap.registerPlugin(useGSAP);

export type SplitLine = { text: string; gradient?: boolean };

export function SplitHeading({ lines, delay = 0.1 }: { lines: SplitLine[]; delay?: number }) {
  const root = useRef<HTMLHeadingElement>(null);

  useGSAP(
    () => {
      if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
      const chars = root.current?.querySelectorAll(".split-char");
      if (!chars?.length) return;
      gsap.set(chars, { yPercent: 115, rotate: 6 });
      return whenSplashDone(() => {
        gsap.to(chars, {
          yPercent: 0,
          rotate: 0,
          duration: 0.9,
          ease: "expo.out",
          stagger: 0.022,
          delay,
        });
      });
    },
    { scope: root },
  );

  return (
    <h1 ref={root} aria-label={lines.map((l) => l.text).join(" ")}>
      {lines.map((line, li) => (
        <span key={li} className="split-line" aria-hidden="true">
          {line.text.split(" ").map((word, wi, words) => (
            <span key={wi} className={`split-word${line.gradient ? " gradient-text" : ""}`}>
              {word.split("").map((c, ci) => (
                <span key={ci} className={`split-char${line.gradient ? " gradient-text" : ""}`}>
                  {c}
                </span>
              ))}
              {wi < words.length - 1 ? "\u00a0" : null}
            </span>
          ))}
        </span>
      ))}
    </h1>
  );
}

/** Number that counts up from zero when it first renders a value. */
export function CountUp({ value }: { value: number }) {
  const el = useRef<HTMLSpanElement>(null);
  useGSAP(
    () => {
      const node = el.current;
      if (!node) return;
      if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
        node.textContent = String(value);
        return;
      }
      const o = { v: 0 };
      return whenSplashDone(() => {
        gsap.to(o, {
          v: value,
          duration: 1.2,
          ease: "power3.out",
          onUpdate: () => {
            node.textContent = String(Math.round(o.v));
          },
        });
      });
    },
    { dependencies: [value], scope: el, revertOnUpdate: true },
  );
  return <span ref={el}>0</span>;
}
