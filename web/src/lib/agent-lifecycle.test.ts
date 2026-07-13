import { describe, expect, it } from "vitest";
import type { AgentRecord } from "./app-types";
import { agentAction, agentStatusLabel } from "./agent-lifecycle";

function live(status: AgentRecord["status"]): AgentRecord {
  return {
    id: "agent-id",
    strategyVersion: "basis-aapl-v1",
    mode: "live",
    status,
    createdAt: "2026-07-13T10:00:00Z",
    updatedAt: "2026-07-13T10:00:00Z",
  };
}

describe("mainnet agent lifecycle", () => {
  it("provisions before launch", () => {
    expect(agentAction(live("setup"))).toEqual({ kind: "provision", label: "Set up execution" });
    expect(agentAction(live("provisioning"))).toBeNull();
  });

  it("uses commands only at actionable states", () => {
    expect(agentAction(live("ready"))).toMatchObject({ kind: "command", command: "launch" });
    expect(agentAction(live("running"))).toMatchObject({ kind: "command", command: "pause" });
    expect(agentAction(live("paused"))).toMatchObject({ kind: "command", command: "resume" });
    expect(agentAction(live("blocked"))).toBeNull();
  });

  it("formats lifecycle states for users", () => {
    expect(agentStatusLabel("awaiting_funding")).toBe("awaiting funding");
  });
});
