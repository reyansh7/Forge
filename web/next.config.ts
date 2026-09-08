import type { NextConfig } from "next";

// The dashboard talks only to the Go API. Rewrites keep Docker/Caddy
// out of the browser. Loopback only — this is not a production bind.
const nextConfig: NextConfig = {
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
