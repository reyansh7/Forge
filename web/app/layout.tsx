import type { Metadata } from "next";
import { Fraunces, Outfit, IBM_Plex_Mono } from "next/font/google";
import Link from "next/link";
import "./globals.css";
import { HealthDot } from "./health";

const display = Fraunces({
  subsets: ["latin"],
  variable: "--font-display",
});

const sans = Outfit({
  subsets: ["latin"],
  variable: "--font-sans",
});

const mono = IBM_Plex_Mono({
  subsets: ["latin"],
  weight: ["400", "500"],
  variable: "--font-mono",
});

export const metadata: Metadata = {
  title: "Forge",
  description: "Local Forge dashboard",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={`${display.variable} ${sans.variable} ${mono.variable}`}>
      <body style={{ fontFamily: "var(--font-sans), system-ui, sans-serif" }}>
        <div className="shell">
          <header className="topbar">
            <Link href="/" className="brand">
              <span className="mark" aria-hidden="true" />
              <span>
                <span className="brand-name">Forge</span>
                <span className="brand-sub">Local platform</span>
              </span>
            </Link>
            <HealthDot />
          </header>
          {children}
        </div>
      </body>
    </html>
  );
}
