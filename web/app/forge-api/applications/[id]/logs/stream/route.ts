import { NextRequest } from "next/server";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const idOK = /^[0-9a-fA-F-]{36}$/;

// Proxy SSE to the Go API. Next.js rewrites can buffer an external
// follow stream; this route copies the body so the dashboard EventSource
// stays on 127.0.0.1:3000 and still sends the session cookie.
export async function GET(req: NextRequest, ctx: { params: Promise<{ id: string }> }) {
  const { id } = await ctx.params;
  if (!idOK.test(id)) {
    return Response.json({ error: "invalid application id" }, { status: 400 });
  }
  const tail = req.nextUrl.searchParams.get("tail") || "200";
  const headers = new Headers();
  const cookie = req.headers.get("cookie");
  const auth = req.headers.get("authorization");
  if (cookie) {
    headers.set("cookie", cookie);
  }
  if (auth) {
    headers.set("authorization", auth);
  }
  const up = await fetch(`http://127.0.0.1:8080/applications/${id}/logs/stream?tail=${encodeURIComponent(tail)}`, {
    headers,
  });
  const out = new Headers();
  out.set("Content-Type", up.headers.get("Content-Type") || "text/event-stream");
  out.set("Cache-Control", "no-cache");
  out.set("Connection", "keep-alive");
  return new Response(up.body, { status: up.status, headers: out });
}
