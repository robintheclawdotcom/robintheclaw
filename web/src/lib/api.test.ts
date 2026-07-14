import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppApi } from "./api";

beforeEach(() => {
  const values = new Map<string, string>();
  vi.stubGlobal("window", {
    localStorage: {
      clear: () => values.clear(),
      getItem: (key: string) => values.get(key) ?? null,
      removeItem: (key: string) => values.delete(key),
      setItem: (key: string, value: string) => values.set(key, value),
    },
  });
});

afterEach(() => {
  window.localStorage.clear();
  vi.unstubAllGlobals();
});

describe("mainnet agent API", () => {
  it("creates only the fixed live strategy", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: "agent-id" }), {
      status: 201,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetch);
    const api = new AppApi(async () => "access-token");

    await api.launchAgent();

    expect(fetch).toHaveBeenCalledWith("/api/app/v1/agents", expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ strategyVersion: "basis-aapl-v1" }),
    }));
  });

  it("sends an idempotency key with lifecycle commands", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: "command-id" }), {
      status: 202,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetch);
    const api = new AppApi(async () => null);

    await api.agentCommand("agent-id", "pause", "stable-command-key");

    expect(fetch).toHaveBeenCalledWith("/api/app/v1/agents/agent-id/commands", expect.objectContaining({
      method: "POST",
      headers: expect.objectContaining({ "Idempotency-Key": "stable-command-key" }),
      body: JSON.stringify({ command: "pause" }),
    }));
  });

  it("lets the server discover the Lighter account and nonce", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: "awaiting_signature" }), {
      status: 201,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetch);
    const api = new AppApi(async () => "access-token");
    const ownerAddress = "0x1111111111111111111111111111111111111111";

    await api.requestLighterLink("agent-id", { ownerAddress });

    expect(fetch).toHaveBeenCalledWith("/api/app/v1/agents/agent-id/lighter/link-request", expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ ownerAddress }),
    }));
  });

  it("reuses the idempotency key while a command is pending", async () => {
    const fetch = vi.fn().mockImplementation(async () => new Response(JSON.stringify({
        id: "command-id",
        command: "pause",
        status: "pending",
      }), {
        status: 202,
        headers: { "Content-Type": "application/json" },
      }));
    vi.stubGlobal("fetch", fetch);
    const api = new AppApi(async () => null);

    await api.agentCommand("agent-id", "pause");
    await api.agentCommand("agent-id", "pause");

    const first = fetch.mock.calls[0][1]?.headers as Record<string, string>;
    const second = fetch.mock.calls[1][1]?.headers as Record<string, string>;
    expect(first["Idempotency-Key"]).toBe(second["Idempotency-Key"]);
  });

  it("recovers a pending command and clears it after terminal evidence", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        id: "command-id",
        command: "pause",
        status: "processing",
      }), {
        status: 202,
        headers: { "Content-Type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        id: "command-id",
        command: "pause",
        status: "completed",
      }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }));
    vi.stubGlobal("fetch", fetch);
    const api = new AppApi(async () => null);

    await api.agentCommand("agent-id", "pause");
    expect(new AppApi(async () => null).pendingAgentCommand("agent-id", "pause")).toBe("command-id");

    await api.getAgentCommand("agent-id", "command-id");
    expect(api.pendingAgentCommand("agent-id", "pause")).toBeNull();
  });

  it("confirms Robinhood deployment without accepting graph addresses", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: "linked" }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetch);
    const api = new AppApi(async () => "access-token");

    await api.confirmRobinhood("agent-id", {
      requestId: "request-id",
      transactionHash: `0x${"ab".repeat(32)}`,
    });

    expect(fetch).toHaveBeenCalledWith("/api/app/v1/agents/agent-id/robinhood/confirm", expect.objectContaining({
      method: "POST",
      body: JSON.stringify({
        requestId: "request-id",
        transactionHash: `0x${"ab".repeat(32)}`,
      }),
    }));
  });
});
