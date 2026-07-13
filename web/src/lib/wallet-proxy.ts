import { decodeFunctionData, getAddress, parseAbi, type Address, type Hex } from "viem";

const chainId = 46_630;
const controlAbi = parseAbi([
  "function approve(address spender,uint256 amount) returns (bool)",
  "function deposit(uint256 amount)",
  "function withdraw(address to,uint256 amount)",
  "function setHalted(bool halted)",
]);

export type WalletRpcRequest = {
  jsonrpc: "2.0";
  id: string | number | null;
  method: "wallet_prepareCalls" | "wallet_sendPreparedCalls" | "wallet_getCallsStatus" | "eth_getTransactionReceipt";
  params: unknown[];
};

type RpcCall = { to: Address; data: Hex; value: bigint };
type Dashboard = {
  smartAccount: { address: Address } | null;
  vault: {
    record: {
      assetAddress: Address;
      vaultAddress: Address;
      guardAddress: Address;
    };
  } | null;
  wallets: Array<{ wallet: { address: Address } }>;
};
type TransactionPlan = {
  smartAccount: Address;
  calls: Array<{ to: Address; data: Hex; value: string }>;
};

export class WalletProxyError extends Error {
  constructor(readonly status: number, readonly code: string, message: string) {
    super(message);
  }
}

export function parseWalletRpc(input: unknown): WalletRpcRequest {
  if (!input || typeof input !== "object" || Array.isArray(input)) {
    throw new WalletProxyError(400, "invalid_rpc", "A single JSON-RPC request is required.");
  }
  const request = input as Record<string, unknown>;
  const methods = new Set([
    "wallet_prepareCalls",
    "wallet_sendPreparedCalls",
    "wallet_getCallsStatus",
    "eth_getTransactionReceipt",
  ]);
  if (request.jsonrpc !== "2.0" || !methods.has(String(request.method)) || !Array.isArray(request.params)) {
    throw new WalletProxyError(403, "rpc_method_denied", "This wallet operation is not allowed.");
  }
  if (!(typeof request.id === "string" || typeof request.id === "number" || request.id === null)) {
    throw new WalletProxyError(400, "invalid_rpc", "The JSON-RPC request ID is invalid.");
  }
  return request as WalletRpcRequest;
}

export function injectSponsorship(request: WalletRpcRequest, policyId: string): WalletRpcRequest {
  if (request.method !== "wallet_prepareCalls" && request.method !== "wallet_sendPreparedCalls") return request;
  const first = request.params[0];
  if (!first || typeof first !== "object" || Array.isArray(first)) {
    throw new WalletProxyError(400, "invalid_rpc", "Wallet call parameters are invalid.");
  }
  return {
    ...request,
    params: [{
      ...(first as Record<string, unknown>),
      capabilities: {
        ...capabilitiesWithoutPaymaster((first as Record<string, unknown>).capabilities),
        paymasterService: { policyId },
      },
    }, ...request.params.slice(1)],
  };
}

export async function authorizePreparedCalls(request: WalletRpcRequest, token: string, requestId: string) {
  if (request.method !== "wallet_prepareCalls") return;
  const input = request.params[0] as Record<string, unknown> | undefined;
  const from = address(input?.from);
  if (!from || !isRobinhoodChain(input?.chainId)) {
    throw new WalletProxyError(400, "invalid_call", "The wallet request must target Robinhood Chain testnet.");
  }
  const calls = rpcCalls(input?.calls);
  const dashboard = await appRequest<Dashboard>("/api/v1/dashboard", "GET", token, requestId);
  if (!dashboard.vault) {
    const plan = await appRequest<TransactionPlan>("/api/v1/vaults/prepare", "POST", token, requestId);
    if (!sameAddress(plan.smartAccount, from) || !sameCalls(calls, plan.calls)) {
      throw new WalletProxyError(403, "call_not_authorized", "The onboarding batch does not match the verified vault plan.");
    }
    return;
  }

  const owner = dashboard.smartAccount?.address;
  if (!owner) throw new WalletProxyError(409, "account_not_ready", "The strategy account is not ready.");
  const linked = new Set([owner, ...dashboard.wallets.map(({ wallet }) => wallet.address)].map(normalizeAddress));
  if (!linked.has(normalizeAddress(from))) {
    throw new WalletProxyError(403, "wallet_not_linked", "The signing wallet is not linked to this account.");
  }
  authorizeControlCalls(calls, from, owner, dashboard.vault.record);
}

function authorizeControlCalls(
  calls: RpcCall[],
  from: Address,
  owner: Address,
  vault: Dashboard["vault"] extends { record: infer R } | null ? R : never,
) {
  if (calls.length === 1 && sameAddress(calls[0].to, vault.guardAddress)) {
    requireOwner(from, owner);
    decode(calls[0], "setHalted");
    return;
  }
  if (calls.length === 1 && sameAddress(calls[0].to, vault.vaultAddress)) {
    requireOwner(from, owner);
    const decoded = decode(calls[0], "withdraw");
    if (!sameAddress(String(decoded.args[0]), owner) || BigInt(decoded.args[1] as bigint) <= 0n) {
      throw new WalletProxyError(403, "call_not_authorized", "Withdrawals must return funds to the strategy account.");
    }
    return;
  }
  if (calls.length === 2 && sameAddress(calls[0].to, vault.assetAddress) && sameAddress(calls[1].to, vault.vaultAddress)) {
    const approval = decode(calls[0], "approve");
    const deposit = decode(calls[1], "deposit");
    if (!sameAddress(String(approval.args[0]), vault.vaultAddress) || BigInt(approval.args[1] as bigint) !== BigInt(deposit.args[0] as bigint) || BigInt(deposit.args[0] as bigint) <= 0n) {
      throw new WalletProxyError(403, "call_not_authorized", "The funding batch is not valid for this vault.");
    }
    return;
  }
  throw new WalletProxyError(403, "call_not_authorized", "This sponsored operation is outside the strategy mandate.");
}

function decode(call: RpcCall, expected: "approve" | "deposit" | "withdraw" | "setHalted") {
  if (call.value !== 0n) throw new WalletProxyError(403, "call_not_authorized", "Native-value transfers are not sponsored.");
  try {
    const decoded = decodeFunctionData({ abi: controlAbi, data: call.data });
    if (decoded.functionName !== expected) throw new Error("selector mismatch");
    return decoded;
  } catch {
    throw new WalletProxyError(403, "call_not_authorized", "This contract call is not sponsored.");
  }
}

function requireOwner(from: Address, owner: Address) {
  if (!sameAddress(from, owner)) {
    throw new WalletProxyError(403, "owner_required", "Only the strategy account can authorize this operation.");
  }
}

function rpcCalls(value: unknown): RpcCall[] {
  if (!Array.isArray(value) || value.length < 1 || value.length > 8) {
    throw new WalletProxyError(400, "invalid_call", "The wallet batch must contain between one and eight calls.");
  }
  return value.map((raw) => {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw new WalletProxyError(400, "invalid_call", "A wallet call is invalid.");
    const call = raw as Record<string, unknown>;
    const to = address(call.to);
    if (!to || typeof call.data !== "string" || !/^0x[0-9a-fA-F]*$/.test(call.data)) {
      throw new WalletProxyError(400, "invalid_call", "A wallet call target or calldata is invalid.");
    }
    try {
      return { to, data: call.data as Hex, value: BigInt(String(call.value ?? "0")) };
    } catch {
      throw new WalletProxyError(400, "invalid_call", "A wallet call value is invalid.");
    }
  });
}

async function appRequest<T>(path: string, method: "GET" | "POST", token: string, requestId: string): Promise<T> {
  const rawBase = process.env.APP_API_BASE_URL;
  if (!rawBase) throw new WalletProxyError(503, "api_unavailable", "The application API is not configured.");
  const base = /^https?:\/\//.test(rawBase) ? rawBase : `http://${rawBase}`;
  const response = await fetch(new URL(path, base), {
    method,
    headers: { Authorization: `Bearer ${token}`, "X-Request-Id": requestId, Accept: "application/json" },
    cache: "no-store",
    redirect: "manual",
    signal: AbortSignal.timeout(15_000),
  });
  const payload = await response.json().catch(() => null) as T | { message?: string } | null;
  if (!response.ok) {
    const message = payload && typeof payload === "object" && "message" in payload && typeof payload.message === "string"
      ? payload.message
      : "The application API could not authorize this operation.";
    throw new WalletProxyError(response.status, "authorization_failed", message);
  }
  return payload as T;
}

function capabilitiesWithoutPaymaster(value: unknown) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const { paymasterService: _, paymaster: __, ...capabilities } = value as Record<string, unknown>;
  return capabilities;
}

function sameCalls(actual: RpcCall[], expected: TransactionPlan["calls"]) {
  if (actual.length !== expected.length) return false;
  return actual.every((call, index) => {
    const planned = expected[index];
    return sameAddress(call.to, planned.to)
      && call.data.toLowerCase() === planned.data.toLowerCase()
      && call.value === BigInt(planned.value);
  });
}

function isRobinhoodChain(value: unknown) {
  try {
    return typeof value === "string" ? Number(BigInt(value)) === chainId : value === chainId;
  } catch {
    return false;
  }
}

function address(value: unknown): Address | null {
  if (typeof value !== "string") return null;
  try {
    return getAddress(value);
  } catch {
    return null;
  }
}

function normalizeAddress(value: string) {
  return value.toLowerCase();
}

function sameAddress(left: string, right: string) {
  return normalizeAddress(left) === normalizeAddress(right);
}
