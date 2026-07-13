import { describe, expect, it } from "vitest";
import { configureSponsorship, parseWalletRpc, WalletProxyError } from "./wallet-proxy";

describe("wallet proxy", () => {
  it("accepts only the required wallet methods", () => {
    const request = parseWalletRpc({ jsonrpc: "2.0", id: 1, method: "wallet_getCallsStatus", params: ["0x01"] });
    expect(request.method).toBe("wallet_getCallsStatus");
    expect(() => parseWalletRpc({ jsonrpc: "2.0", id: 1, method: "eth_sendTransaction", params: [] }))
      .toThrow(WalletProxyError);
    expect(() => parseWalletRpc([{ jsonrpc: "2.0", id: 1, method: "wallet_getCallsStatus", params: [] }]))
      .toThrow("single JSON-RPC request");
  });

  it("replaces client paymaster data with the server policy", () => {
    const request = parseWalletRpc({
      jsonrpc: "2.0",
      id: 2,
      method: "wallet_prepareCalls",
      params: [{ capabilities: { paymasterService: { policyId: "client" }, auxiliary: true } }],
    });
    expect(configureSponsorship(request, "server").params[0]).toEqual({
      capabilities: { auxiliary: true, paymasterService: { policyId: "server" } },
    });
  });

  it("removes client paymaster data when sponsorship is disabled", () => {
    const request = parseWalletRpc({
      jsonrpc: "2.0",
      id: 3,
      method: "wallet_prepareCalls",
      params: [{ capabilities: { paymasterService: { policyId: "client" }, paymaster: { policyId: "client" }, auxiliary: true } }],
    });
    expect(configureSponsorship(request).params[0]).toEqual({
      capabilities: { auxiliary: true },
    });
  });
});
