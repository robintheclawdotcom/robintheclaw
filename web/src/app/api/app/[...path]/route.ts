import { randomUUID } from "node:crypto";
import { NextRequest, NextResponse } from "next/server";
import {
  BodyTooLargeError,
  readBoundedBody,
  validateContentLength,
} from "../../../../lib/server-body";
import { isSameOriginRequest } from "../../../../lib/server-origin";
import { requestSubject, takeRateLimit } from "../../../../lib/server-rate-limit";

export const dynamic = "force-dynamic";

type RouteContext = { params: Promise<{ path: string[] }> };
const maxBodyBytes = 256 * 1_024;

async function proxy(request: NextRequest, context: RouteContext) {
  const requestId = request.headers.get("x-request-id") ?? randomUUID();
  if (!isSameOriginRequest(request)) {
    return NextResponse.json({ error: "invalid_origin", message: "Request origin is not allowed." }, { status: 403 });
  }
  const auth = request.headers.get("authorization");
  const session = request.cookies.get("privy-token")?.value;
  const subject = auth ?? session ?? requestSubject(request.headers);
  const limit = takeRateLimit("app-api", subject, 120, 60_000);
  if (!limit.allowed) {
    return NextResponse.json(
      { error: "rate_limited", message: "Too many application requests." },
      { status: 429, headers: { "Retry-After": String(limit.retryAfter), "X-Request-Id": requestId } },
    );
  }
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
  for (const name of ["authorization", "content-type", "idempotency-key"]) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }
  headers.set("x-request-id", requestId);
  if (session) headers.set("cookie", `privy-token=${encodeURIComponent(session)}`);

  let body: Uint8Array | undefined;
  if (request.method !== "GET" && request.method !== "HEAD") {
    try {
      validateContentLength(request.headers.get("content-length"), maxBodyBytes);
      body = await readBoundedBody(request.body, maxBodyBytes);
    } catch (error) {
      if (error instanceof BodyTooLargeError) {
        return NextResponse.json(
          { error: "request_too_large", message: "Application request is too large." },
          { status: 413, headers: { "X-Request-Id": requestId } },
        );
      }
      throw error;
    }
  }

  let response: Response;
  try {
    response = await fetch(target, {
      method: request.method,
      headers,
      body: body
        ? body.buffer.slice(body.byteOffset, body.byteOffset + body.byteLength) as ArrayBuffer
        : undefined,
      redirect: "manual",
      cache: "no-store",
      signal: AbortSignal.timeout(30_000),
    });
  } catch {
    console.error(JSON.stringify({ level: "error", event: "app_proxy_failed", requestId }));
    return NextResponse.json(
      { error: "service_unavailable", message: "Application API is unavailable." },
      { status: 503, headers: { "X-Request-Id": requestId } },
    );
  }
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
