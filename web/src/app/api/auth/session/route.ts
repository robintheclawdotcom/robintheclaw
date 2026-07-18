import { NextRequest, NextResponse } from "next/server";
import {
  SessionConfigurationError,
  SessionValidationError,
  verifyPrivySession,
} from "../../../../lib/server-auth";
import {
  BodyTooLargeError,
  readBoundedBody,
  validateContentLength,
} from "../../../../lib/server-body";
import { isSameOriginRequest } from "../../../../lib/server-origin";
import { requestSubject, takeRateLimit } from "../../../../lib/server-rate-limit";

const COOKIE = "privy-token";
const maxBodyBytes = 16 * 1_024;

export async function POST(request: NextRequest) {
  if (!isSameOriginRequest(request)) return NextResponse.json({ error: "invalid_origin" }, { status: 403 });
  if (!request.headers.get("content-type")?.toLowerCase().startsWith("application/json")) {
    return NextResponse.json({ error: "unsupported_media_type" }, { status: 415 });
  }
  const limit = takeRateLimit("session", requestSubject(request.headers), 20, 60_000);
  if (!limit.allowed) {
    return NextResponse.json(
      { error: "rate_limited", message: "Too many sign-in attempts." },
      { status: 429, headers: { "Retry-After": String(limit.retryAfter) } },
    );
  }
  let body: { token?: unknown } | null = null;
  try {
    validateContentLength(request.headers.get("content-length"), maxBodyBytes);
    const bytes = await readBoundedBody(request.body, maxBodyBytes);
    body = JSON.parse(Buffer.from(bytes ?? []).toString("utf8")) as { token?: unknown };
  } catch (error) {
    if (error instanceof BodyTooLargeError) {
      return NextResponse.json({ error: "request_too_large" }, { status: 413 });
    }
  }
  if (typeof body?.token !== "string" || body.token.length < 20 || body.token.length > 8_192) {
    return NextResponse.json({ error: "invalid_token" }, { status: 400 });
  }
  try {
    await verifyPrivySession(body.token);
  } catch (error) {
    if (error instanceof SessionConfigurationError) {
      return NextResponse.json(
        { error: "service_unavailable", message: "Session verification is not configured." },
        { status: 503 },
      );
    }
    if (error instanceof SessionValidationError) {
      return NextResponse.json({ error: "invalid_token", message: "Session token is invalid or expired." }, { status: 401 });
    }
    throw error;
  }
  const response = NextResponse.json({ ok: true });
  response.cookies.set(COOKIE, body.token, {
    httpOnly: true,
    secure: process.env.NODE_ENV === "production",
    sameSite: "lax",
    path: "/",
    maxAge: 60 * 60,
  });
  return response;
}

export async function DELETE(request: NextRequest) {
  if (!isSameOriginRequest(request)) return NextResponse.json({ error: "invalid_origin" }, { status: 403 });
  const response = NextResponse.json({ ok: true });
  response.cookies.set(COOKIE, "", {
    httpOnly: true,
    secure: process.env.NODE_ENV === "production",
    sameSite: "lax",
    path: "/",
    maxAge: 0,
  });
  return response;
}
