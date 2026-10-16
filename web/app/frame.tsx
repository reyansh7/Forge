"use client";

// App shell + global motion system.
//
// Owns the ambient background, sticky top bar, scroll progress bar, and
// the reveal-on-scroll behaviour every page gets for free. Pages do not
// import animation code: any element matching REVEAL fades and lifts in
// when it scrolls into view, including elements React adds later (a
// project list that finishes loading, a tab panel that opens). A
// MutationObserver feeds new nodes into ScrollTrigger.batch.
//
// Cleanup: everything is created inside useGSAP (or wrapped with
// contextSafe), so a route change reverts tweens, kills ScrollTriggers,
// and removes inline styles before the next page sets up its own.

import { useRef } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";
import { useGSAP } from "@gsap/react";
import { HealthDot } from "./health";
import { Logo } from "./logo";
import { SessionBar } from "./session";
import { whenSplashDone } from "./splash";

gsap.registerPlugin(useGSAP, ScrollTrigger);

const REVEAL =
  ".page-head, .crumb, .card, .stat, .project-row, .deploy-split, .tabs, .banner, .metric-row > *, .auth-panel, .pv, .feature-chip, .eyebrow, .lede";
const SPOTLIGHT = ".card, .stat, .project-row";

export function Frame({ children }: { children: React.ReactNode }) {
  const root = useRef<HTMLDivElement>(null);
  const pathname = usePathname();

  useGSAP(
    (_ctx, contextSafe) => {
      const el = root.current;
      if (!el || !contextSafe) return;
      const topbar = el.querySelector(".topbar");

      // Glass top bar once the page has scrolled at all.
      ScrollTrigger.create({
        start: 8,
        end: "max",
        onToggle: (self) => topbar?.classList.toggle("scrolled", self.isActive),
      });

      const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
      if (reduced) return;

      gsap.to(".scroll-progress", {
        scaleX: 1,
        ease: "none",
        scrollTrigger: { start: 0, end: "max", scrub: 0.3 },
      });
      gsap.to(".orb-a", { yPercent: 35, ease: "none", scrollTrigger: { start: 0, end: "max", scrub: 1.2 } });
      gsap.to(".orb-b", { yPercent: -25, xPercent: 15, ease: "none", scrollTrigger: { start: 0, end: "max", scrub: 1.5 } });
      gsap.to(".orb-c", { yPercent: -40, ease: "none", scrollTrigger: { start: 0, end: "max", scrub: 1 } });
      gsap.to(".bg-grid", { yPercent: 8, ease: "none", scrollTrigger: { start: 0, end: "max", scrub: true } });

      // Pointer glow: quickTo reuses one tween instead of creating one
      // per mousemove event.
      const glowX = gsap.quickTo(".pointer-glow", "x", { duration: 0.7, ease: "power3" });
      const glowY = gsap.quickTo(".pointer-glow", "y", { duration: 0.7, ease: "power3" });
      const onMove = (e: PointerEvent) => {
        glowX(e.clientX);
        glowY(e.clientY);
        const card = (e.target as Element | null)?.closest?.(SPOTLIGHT) as HTMLElement | null;
        if (card) {
          const r = card.getBoundingClientRect();
          card.style.setProperty("--mx", `${e.clientX - r.left}px`);
          card.style.setProperty("--my", `${e.clientY - r.top}px`);
        }
      };
      window.addEventListener("pointermove", onMove, { passive: true });

      const main = el.querySelector("main");
      const seen = new WeakSet<Element>();

      const reveal = contextSafe((nodes: Element[]) => {
        const fresh = nodes.filter((n) => !seen.has(n) && !n.closest(".splash"));
        fresh.forEach((n) => seen.add(n));
        if (!fresh.length) return;
        gsap.set(fresh, { autoAlpha: 0, y: 28 });
        ScrollTrigger.batch(fresh, {
          start: "top 94%",
          once: true,
          onEnter: (batch) =>
            gsap.to(batch, {
              autoAlpha: 1,
              y: 0,
              duration: 0.8,
              ease: "power3.out",
              stagger: 0.07,
              overwrite: true,
              clearProps: "transform",
            }),
        });
      }) as (nodes: Element[]) => void;

      const collect = (node: Node): Element[] => {
        if (!(node instanceof Element)) return [];
        const out = node.matches(REVEAL) ? [node] : [];
        return out.concat(Array.from(node.querySelectorAll(REVEAL)));
      };

      let observer: MutationObserver | null = null;
      const stopWaiting = whenSplashDone(() => {
        if (!main) return;
        reveal(collect(main).filter((n) => n !== main));
        observer = new MutationObserver((records) => {
          const added: Element[] = [];
          for (const rec of records) rec.addedNodes.forEach((n) => added.push(...collect(n)));
          if (added.length) reveal(added);
        });
        observer.observe(main, { childList: true, subtree: true });
      });

      return () => {
        stopWaiting();
        observer?.disconnect();
        window.removeEventListener("pointermove", onMove);
      };
    },
    { scope: root, dependencies: [pathname], revertOnUpdate: true },
  );

  return (
    <div ref={root} className="frame">
      <div className="bg-layer" aria-hidden="true">
        <div className="bg-grid" />
        <div className="orb orb-a" />
        <div className="orb orb-b" />
        <div className="orb orb-c" />
        <div className="bg-noise" />
      </div>
      <div className="pointer-glow" aria-hidden="true" />
      <div className="scroll-progress" aria-hidden="true" />
      <header className="topbar">
        <div className="topbar-inner">
          <Link href="/" className="brand">
            <Logo className="brand-logo" />
            <span>
              <span className="brand-name">Forge</span>
              <span className="brand-sub">Self-hosted platform</span>
            </span>
          </Link>
          <div className="top-meta">
            <SessionBar />
            <HealthDot />
          </div>
        </div>
      </header>
      <main className="shell">{children}</main>
    </div>
  );
}
