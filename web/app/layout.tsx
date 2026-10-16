import type { Metadata } from "next";
import { IBM_Plex_Mono, Inter, Space_Grotesk } from "next/font/google";
import "./globals.css";
import { Frame } from "./frame";
import { Splash } from "./splash";

const mono = IBM_Plex_Mono({
  subsets: ["latin"],
  weight: ["400", "500"],
  variable: "--font-mono",
});

const body = Inter({
  subsets: ["latin"],
  variable: "--font-body",
});

const display = Space_Grotesk({
  subsets: ["latin"],
  weight: ["500", "600", "700"],
  variable: "--font-display",
});

export const metadata: Metadata = {
  title: "Forge",
  description: "Self-hosted deployment platform",
};

// Runs before first paint. If this tab already watched the splash (or
// the OS prefers reduced motion), mark it seen so CSS hides the overlay
// immediately instead of flashing it and then removing it.
const splashGate = `try{if(sessionStorage.getItem("forge:splash")||matchMedia("(prefers-reduced-motion: reduce)").matches){document.documentElement.dataset.splash="seen"}}catch(e){}`;

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={`${mono.variable} ${body.variable} ${display.variable}`} suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: splashGate }} />
      </head>
      <body>
        <Splash />
        <Frame>{children}</Frame>
      </body>
    </html>
  );
}
