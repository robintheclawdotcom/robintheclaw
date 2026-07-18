import { randomUUID } from "node:crypto";
import { NextRequest, NextResponse } from "next/server";
import {
  SessionConfigurationError,
  SessionValidationError,
  verifyPrivySession,
} from "../../../lib/server-auth";
import {
  BodyTooLargeError,
  readBoundedBody,
  validateContentLength,
} from "../../../lib/server-body";
import { isSameOriginRequest } from "../../../lib/server-origin";
import { takeRateLimit } from "../../../lib/server-rate-limit";
import {
  authorizePreparedCalls,
  parseWalletRpc,
  removeSponsorship,
  WalletProxyError,
} from "../../../lib/wallet-proxy";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const maxBodyBytes = 256 * 1_024;

export async function GET(request: NextRequest) {
  const requestId = request.headers.get("x-request-id") ?? randomUUID();
  try {
    const token = accessToken(request);
    if (!token) throw new WalletProxyError(401, "authentication_required", "Sign in to continue.");
    const session = await verifyPrivySession(token);
    const limit = takeRateLimit("wallet-status", session.sessionId, 30, 60_000);
    if (!limit.allowed) return jsonError(429, "rate_limited", "Too many wallet status requests.", requestId, limit.retryAfter);

    const address = request.nextUrl.searchParams.get("address");
    if (!address || !/^0x[0-9a-fA-F]{40}$/.test(address)) {
      throw new WalletProxyError(400, "invalid_address", "A valid strategy account address is required.");
    }
    const response = await fetch(balanceRpcUrl(), {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: requestId, method: "eth_getBalance", params: [address, "latest"] }),
      cache: "no-store",
      redirect: "manual",
      signal: AbortSignal.timeout(15_000),
    });
    const payload = await response.json().catch(() => null) as { result?: unknown; error?: unknown } | null;
    if (!response.ok || payload?.error || typeof payload?.result !== "string" || !/^0x[0-9a-fA-F]+$/.test(payload.result)) {
      throw new WalletProxyError(502, "wallet_provider_error", "The wallet provider could not read the gas balance.");
    }
    return NextResponse.json({ sponsored: false, balance: payload.result }, {
      headers: { "Cache-Control": "no-store", "X-Request-Id": requestId },
    });
  } catch (error) {
    const failure = error instanceof WalletProxyError
      ? error
      : error instanceof SessionConfigurationError
        ? new WalletProxyError(503, "authentication_unavailable", "Session verification is not configured.")
        : error instanceof SessionValidationError
          ? new WalletProxyError(401, "invalid_session", "Your session is invalid or expired.")
          : error instanceof DOMException && error.name === "TimeoutError"
            ? new WalletProxyError(504, "wallet_timeout", "The wallet provider timed out.")
            : new WalletProxyError(502, "wallet_proxy_error", "Wallet status could not be loaded.");
    return jsonError(failure.status, failure.code, failure.message, requestId);
  }
}

export async function POST(request: NextRequest) {
  const requestId = request.headers.get("x-request-id") ?? randomUUID();
  try {
    if (!isSameOriginRequest(request)) throw new WalletProxyError(403, "invalid_origin", "Request origin is not allowed.");
    if (!request.headers.get("content-type")?.toLowerCase().startsWith("application/json")) {
      throw new WalletProxyError(415, "unsupported_media_type", "JSON is required.");
    }
    try {
      validateContentLength(request.headers.get("content-length"), maxBodyBytes);
    } catch (error) {
      if (error instanceof BodyTooLargeError) {
        throw new WalletProxyError(413, "request_too_large", "Wallet request is too large.");
      }
      throw error;
    }

    const token = request.cookies.get("privy-token")?.value;
    if (!token) throw new WalletProxyError(401, "authentication_required", "Sign in to continue.");
    const session = await verifyPrivySession(token);
    const limit = takeRateLimit("wallet", session.sessionId, 90, 60_000);
    if (!limit.allowed) {
      return jsonError(429, "rate_limited", "Too many wallet requests.", requestId, limit.retryAfter);
    }

    let bytes: Uint8Array | undefined;
    try {
      bytes = await readBoundedBody(request.body, maxBodyBytes);
    } catch (error) {
      if (error instanceof BodyTooLargeError) {
        throw new WalletProxyError(413, "request_too_large", "Wallet request is too large.");
      }
      throw error;
    }
    const text = Buffer.from(bytes ?? []).toString("utf8");
    const rpc = parseWalletRpc(JSON.parse(text));
    if (rpc.method === "wallet_sendPreparedCalls") {
      const sendLimit = takeRateLimit("wallet-send", session.sessionId, 12, 60_000);
      if (!sendLimit.allowed) return jsonError(429, "rate_limited", "Too many submitted wallet operations.", requestId, sendLimit.retryAfter);
    }

    const apiKey = process.env.ALCHEMY_API_KEY;
    if (!apiKey) throw new WalletProxyError(503, "wallet_unavailable", "Wallet operations are not configured.");
    await authorizePreparedCalls(rpc, token, requestId);
    const upstreamRequest = removeSponsorship(rpc);
    const response = await fetch(walletRpcUrl(apiKey), {
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

function accessToken(request: NextRequest) {
  const authorization = request.headers.get("authorization");
  if (authorization?.startsWith("Bearer ")) return authorization.slice(7);
  return request.cookies.get("privy-token")?.value;
}

function walletRpcUrl(apiKey: string) {
  return process.env.ALCHEMY_WALLET_RPC_URL ?? `https://api.g.alchemy.com/v2/${apiKey}`;
}

function balanceRpcUrl() {
  return process.env.RH_MAINNET_RPC
    ?? process.env.APP_RPC_URL
    ?? "https://rpc.mainnet.chain.robinhood.com";
}

function jsonError(status: number, code: string, message: string, requestId: string, retryAfter?: number) {
  const response = NextResponse.json({ error: code, message }, { status, headers: { "X-Request-Id": requestId, "Cache-Control": "no-store" } });
  if (retryAfter) response.headers.set("Retry-After", String(retryAfter));
  return response;
}
