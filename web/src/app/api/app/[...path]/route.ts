import { NextRequest, NextResponse } from "next/server";

export const dynamic = "force-dynamic";

type RouteContext = { params: Promise<{ path: string[] }> };

async function proxy(request: NextRequest, context: RouteContext) {
  const base = process.env.APP_API_BASE_URL;
  if (!base) {
    return NextResponse.json(
      { error: "service_unavailable", message: "Application API is not configured." },
      { status: 503 },
    );
  }

  const { path } = await context.params;
  if (path.some((part) => !/^[a-zA-Z0-9_-]+$/.test(part))) {
    return NextResponse.json({ error: "invalid_request", message: "Invalid API path." }, { status: 400 });
  }

  const origin = /^https?:\/\//.test(base) ? base : `http://${base}`;
  const target = new URL(`/api/${path.join("/")}`, origin);
  target.search = request.nextUrl.search;
  const headers = new Headers({ Accept: "application/json" });
  for (const name of ["authorization", "content-type", "x-request-id"]) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }
  const session = request.cookies.get("privy-token")?.value;
  if (session) headers.set("cookie", `privy-token=${encodeURIComponent(session)}`);

  const response = await fetch(target, {
    method: request.method,
    headers,
    body: request.method === "GET" || request.method === "HEAD" ? undefined : await request.arrayBuffer(),
    redirect: "manual",
    cache: "no-store",
  });
  const outputHeaders = new Headers();
  for (const name of ["content-type", "x-request-id"]) {
    const value = response.headers.get(name);
    if (value) outputHeaders.set(name, value);
  }
  return new NextResponse(response.body, { status: response.status, headers: outputHeaders });
}

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
