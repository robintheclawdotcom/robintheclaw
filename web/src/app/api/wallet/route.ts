import { randomUUID } from "node:crypto";
import { NextRequest, NextResponse } from "next/server";
import {
  SessionConfigurationError,
  SessionValidationError,
  verifyPrivySession,
} from "../../../lib/server-auth";
import { takeRateLimit } from "../../../lib/server-rate-limit";
import {
  authorizePreparedCalls,
  injectSponsorship,
  parseWalletRpc,
  WalletProxyError,
} from "../../../lib/wallet-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const maxBodyBytes = 256 * 1_024;

export async function POST(request: NextRequest) {
  const requestId = request.headers.get("x-request-id") ?? randomUUID();
  try {
    if (!sameOrigin(request)) throw new WalletProxyError(403, "invalid_origin", "Request origin is not allowed.");
    if (!request.headers.get("content-type")?.toLowerCase().startsWith("application/json")) {
      throw new WalletProxyError(415, "unsupported_media_type", "JSON is required.");
    }
    const declaredLength = Number(request.headers.get("content-length") ?? "0");
    if (declaredLength > maxBodyBytes) throw new WalletProxyError(413, "request_too_large", "Wallet request is too large.");

    const token = request.cookies.get("privy-token")?.value;
    if (!token) throw new WalletProxyError(401, "authentication_required", "Sign in to continue.");
    const session = await verifyPrivySession(token);
    const limit = takeRateLimit("wallet", session.sessionId, 90, 60_000);
    if (!limit.allowed) {
      return jsonError(429, "rate_limited", "Too many wallet requests.", requestId, limit.retryAfter);
    }

    const text = await request.text();
    if (Buffer.byteLength(text) > maxBodyBytes) throw new WalletProxyError(413, "request_too_large", "Wallet request is too large.");
    const rpc = parseWalletRpc(JSON.parse(text));
    if (rpc.method === "wallet_sendPreparedCalls") {
      const sendLimit = takeRateLimit("wallet-send", session.sessionId, 12, 60_000);
      if (!sendLimit.allowed) return jsonError(429, "rate_limited", "Too many submitted wallet operations.", requestId, sendLimit.retryAfter);
    }

    const apiKey = process.env.ALCHEMY_API_KEY;
    const policyId = process.env.ALCHEMY_POLICY_ID;
    if (!apiKey || !policyId) throw new WalletProxyError(503, "wallet_unavailable", "Sponsored wallet operations are not configured.");
    await authorizePreparedCalls(rpc, token, requestId);
    const upstreamRequest = injectSponsorship(rpc, policyId);
    const response = await fetch(process.env.ALCHEMY_WALLET_RPC_URL ?? `https://api.g.alchemy.com/v2/${apiKey}`, {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify(upstreamRequest),
      cache: "no-store",
      redirect: "manual",
      signal: AbortSignal.timeout(30_000),
    });
    if (!response.headers.get("content-type")?.includes("application/json")) {
      throw new WalletProxyError(502, "wallet_provider_error", "The wallet provider returned an invalid response.");
    }
    return new NextResponse(response.body, {
      status: response.status,
      headers: { "Content-Type": "application/json", "Cache-Control": "no-store", "X-Request-Id": requestId },
    });
  } catch (error) {
    const failure = error instanceof WalletProxyError
      ? error
      : error instanceof SessionConfigurationError
        ? new WalletProxyError(503, "authentication_unavailable", "Session verification is not configured.")
        : error instanceof SessionValidationError
          ? new WalletProxyError(401, "invalid_session", "Your session is invalid or expired.")
          : error instanceof SyntaxError
            ? new WalletProxyError(400, "invalid_json", "Wallet request is not valid JSON.")
            : error instanceof DOMException && error.name === "TimeoutError"
              ? new WalletProxyError(504, "wallet_timeout", "The wallet provider timed out.")
              : new WalletProxyError(502, "wallet_proxy_error", "The wallet operation could not be completed.");
    console.error(JSON.stringify({ level: "error", event: "wallet_proxy_failed", requestId, code: failure.code, status: failure.status }));
    return jsonError(failure.status, failure.code, failure.message, requestId);
  }
}

function jsonError(status: number, code: string, message: string, requestId: string, retryAfter?: number) {
  const response = NextResponse.json({ error: code, message }, { status, headers: { "X-Request-Id": requestId, "Cache-Control": "no-store" } });
  if (retryAfter) response.headers.set("Retry-After", String(retryAfter));
  return response;
}

function sameOrigin(request: NextRequest) {
  const origin = request.headers.get("origin");
  return !origin || origin === request.nextUrl.origin;
}
