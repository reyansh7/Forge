import type { NextConfig } from "next";

// The dashboard talks only to the Go API. Rewrites keep Docker/Caddy
// out of the browser. Loopback only — this is not a production bind.
const nextConfig: NextConfig = {
  // The corner badge is the development server's own indicator.
  // `next build` / `next start` never render it. Hiding it here keeps
  // `npm run dev` from covering the dashboard while hot reload stays on.
  devIndicators: false,
  async rewrites() {
    return [
      {
        source: "/forge-api/:path*",
        destination: "http://127.0.0.1:8080/:path*",
      },
    ];
  },
};

export default nextConfig;
