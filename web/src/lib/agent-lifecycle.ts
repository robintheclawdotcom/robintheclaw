import type { AgentCommand, AgentRecord, AgentStatus } from "./app-types";

export type AgentAction =
  | { kind: "create"; label: string }
  | { kind: "provision"; label: string }
  | { kind: "paper"; label: string; status: "running" | "paused" }
  | { kind: "command"; label: string; command: AgentCommand }
  | null;

export function agentAction(agent: AgentRecord | null): AgentAction {
  if (!agent) return { kind: "create", label: "Create mainnet agent" };
  if (agent.mode === "paper") {
    return agent.status === "running"
      ? { kind: "paper", label: "Pause agent", status: "paused" }
      : { kind: "paper", label: "Resume agent", status: "running" };
  }
  switch (agent.status) {
    case "setup":
      return { kind: "provision", label: "Set up execution" };
    case "ready":
      return { kind: "command", label: "Launch agent", command: "launch" };
    case "running":
      return { kind: "command", label: "Pause and unwind", command: "pause" };
    case "paused":
      return { kind: "command", label: "Resume agent", command: "resume" };
    default:
      return null;
  }
}

export function agentStatusLabel(status: AgentStatus): string {
  return status.replaceAll("_", " ");
}
