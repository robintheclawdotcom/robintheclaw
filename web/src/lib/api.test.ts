import { afterEach, describe, expect, it, vi } from "vitest";
import { AppApi } from "./api";

afterEach(() => vi.unstubAllGlobals());

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
});
