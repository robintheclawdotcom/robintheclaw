import { NextRequest, NextResponse } from "next/server";
import {
  SessionConfigurationError,
  SessionValidationError,
  verifyPrivySession,
} from "../../../../lib/server-auth";
import { isSameOriginRequest } from "../../../../lib/server-origin";
import { requestSubject, takeRateLimit } from "../../../../lib/server-rate-limit";

const COOKIE = "privy-token";

export async function POST(request: NextRequest) {
  if (!isSameOriginRequest(request)) return NextResponse.json({ error: "invalid_origin" }, { status: 403 });
  const limit = takeRateLimit("session", requestSubject(request.headers), 20, 60_000);
  if (!limit.allowed) {
    return NextResponse.json(
      { error: "rate_limited", message: "Too many sign-in attempts." },
      { status: 429, headers: { "Retry-After": String(limit.retryAfter) } },
    );
  }
  const body = await request.json().catch(() => null) as { token?: unknown } | null;
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
