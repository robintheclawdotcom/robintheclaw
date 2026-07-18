import type {
  ActivityPage,
  AgentCommand,
  AgentCommandRecord,
  AgentExecutionStatus,
  AgentReadiness,
  AgentRecord,
  DashboardSnapshot,
  ExecutionAccountRecord,
  ExecutionBindingRecord,
  LighterLinkRequest,
  LighterRevocation,
  MeResponse,
  PreferencesRecord,
  TransactionPlan,
  VaultRecord,
} from "./app-types";

export class AppApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: string,
    readonly details?: unknown,
  ) {
    super(message);
  }
}

type TokenGetter = () => Promise<string | null>;
type PendingCommand = { key: string; commandId?: string };

export class AppApi {
  private readonly pendingCommands = new Map<string, PendingCommand>();

  constructor(private readonly getAccessToken: TokenGetter) {}

  me(): Promise<MeResponse> {
    return this.request("/v1/me");
  }

  syncWallets(): Promise<MeResponse> {
    return this.request("/v1/me/wallets/sync", { method: "POST" });
  }

  updatePreferences(input: {
    displayCurrency: string;
    activeFundingWallet: string | null;
    notificationsEnabled: boolean;
  }): Promise<PreferencesRecord> {
    return this.request("/v1/me/preferences", {
      method: "PUT",
      body: JSON.stringify(input),
    });
  }

  dashboard(): Promise<DashboardSnapshot> {
    return this.request("/v1/dashboard");
  }

  launchAgent(): Promise<AgentRecord> {
    return this.request("/v1/agents", {
      method: "POST",
      body: JSON.stringify({ strategyVersion: "basis-aapl-v1" }),
    });
  }

  updatePaperAgent(id: string, status: "running" | "paused"): Promise<AgentRecord> {
    return this.request(`/v1/agents/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify({ status }),
    });
  }

  createExecutionAccount(agentId: string): Promise<ExecutionAccountRecord> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/execution-account`, { method: "POST" });
  }

  agentReadiness(agentId: string): Promise<AgentReadiness> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/readiness`);
  }

  agentExecution(agentId: string): Promise<AgentExecutionStatus> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/execution`);
  }

  requestLighterLink(agentId: string, input: LighterLinkRequest): Promise<ExecutionBindingRecord> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/lighter/link-request`, {
      method: "POST",
      body: JSON.stringify(input),
    });
  }

  confirmLighterLink(agentId: string, input: {
    requestId: string;
    linkId: string;
    l1Signature: string;
  }): Promise<ExecutionBindingRecord> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/lighter/confirm`, {
      method: "POST",
      body: JSON.stringify(input),
    });
  }

  async lighterRevocation(agentId: string): Promise<LighterRevocation | null> {
    try {
      return await this.request(`/v1/agents/${encodeURIComponent(agentId)}/lighter/revocation`);
    } catch (error) {
      if (error instanceof AppApiError && [400, 404].includes(error.status)) return null;
      throw error;
    }
  }

  confirmLighterRevocation(agentId: string, input: {
    revocationId: string;
    l1Signature: string;
  }): Promise<LighterRevocation> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/lighter/revocation/confirm`, {
      method: "POST",
      body: JSON.stringify(input),
    });
  }

  prepareRobinhood(agentId: string): Promise<ExecutionBindingRecord> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/robinhood/prepare`, { method: "POST" });
  }

  confirmRobinhood(agentId: string, input: {
    requestId: string;
    transactionHash: string;
  }): Promise<ExecutionBindingRecord> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/robinhood/confirm`, {
      method: "POST",
      body: JSON.stringify(input),
    });
  }

  async agentCommand(agentId: string, command: AgentCommand, idempotencyKey?: string): Promise<AgentCommandRecord> {
    const storageKey = this.commandStorageKey(agentId, command);
    const pending = this.readPendingCommand(storageKey);
    const key = idempotencyKey ?? pending?.key ?? crypto.randomUUID();
    this.writePendingCommand(storageKey, { key, commandId: pending?.commandId });
    let result: AgentCommandRecord;
    try {
      result = await this.request<AgentCommandRecord>(`/v1/agents/${encodeURIComponent(agentId)}/commands`, {
        method: "POST",
        headers: { "Idempotency-Key": key },
        body: JSON.stringify({ command }),
      });
    } catch (error) {
      if (
        error instanceof AppApiError
        && rejectedCommandMatches(error.details, agentId, command, key)
      ) {
        this.clearPendingCommand(storageKey);
      }
      throw error;
    }
    if (this.isTerminalCommand(result)) this.clearPendingCommand(storageKey);
    else this.writePendingCommand(storageKey, { key, commandId: result.id });
    return result;
  }

  async getAgentCommand(agentId: string, commandId: string): Promise<AgentCommandRecord> {
    const result = await this.request<AgentCommandRecord>(`/v1/agents/${encodeURIComponent(agentId)}/commands/${encodeURIComponent(commandId)}`);
    if (this.isTerminalCommand(result)) {
      this.clearPendingCommand(this.commandStorageKey(agentId, result.command));
    }
    return result;
  }

  activeAgentCommand(agentId: string): Promise<AgentCommandRecord | null> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/commands/pending`);
  }

  pendingAgentCommand(agentId: string, command: AgentCommand): string | null {
    return this.readPendingCommand(this.commandStorageKey(agentId, command))?.commandId ?? null;
  }

  activity(cursor?: string): Promise<ActivityPage> {
    const query = cursor ? `?cursor=${encodeURIComponent(cursor)}` : "";
    return this.request(`/v1/activity${query}`);
  }

  prepareVault(): Promise<TransactionPlan> {
    return this.request("/v1/vaults/prepare", { method: "POST" });
  }

  confirmVault(callId: string): Promise<VaultRecord> {
    return this.request("/v1/vaults/confirm", {
      method: "POST",
      body: JSON.stringify({ callId }),
    });
  }

  async metric(name: string, durationMs?: number, status?: string): Promise<void> {
    await this.request("/v1/metrics", {
      method: "POST",
      body: JSON.stringify({ name, durationMs, status }),
    });
  }

  private commandStorageKey(agentId: string, command: AgentCommand) {
    return `robin:agent-command:${agentId}:${command}`;
  }

  private readPendingCommand(key: string): PendingCommand | undefined {
    if (typeof window === "undefined") return this.pendingCommands.get(key);
    try {
      const value = window.localStorage.getItem(key);
      if (!value) return undefined;
      const parsed = JSON.parse(value) as Partial<PendingCommand>;
      if (typeof parsed.key !== "string" || !parsed.key) return undefined;
      return {
        key: parsed.key,
        commandId: typeof parsed.commandId === "string" ? parsed.commandId : undefined,
      };
    } catch {
      return this.pendingCommands.get(key);
    }
  }

  private writePendingCommand(key: string, command: PendingCommand) {
    this.pendingCommands.set(key, command);
    if (typeof window === "undefined") return;
    try {
      window.localStorage.setItem(key, JSON.stringify(command));
    } catch {
      // The in-memory copy still prevents duplicate submissions in this session.
    }
  }

  private clearPendingCommand(key: string) {
    this.pendingCommands.delete(key);
    if (typeof window === "undefined") return;
    try {
      window.localStorage.removeItem(key);
    } catch {
      // Nothing else can be done when browser storage is unavailable.
    }
  }

  private isTerminalCommand(command: AgentCommandRecord) {
    return command.status === "completed" || command.status === "rejected" || command.status === "failed";
  }

  private async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const token = await this.getAccessToken();
    const response = await fetch(`/api/app${path}`, {
      ...init,
      credentials: "include",
      headers: {
        Accept: "application/json",
        ...(init.body ? { "Content-Type": "application/json" } : {}),
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        ...init.headers,
      },
    });
    if (response.status === 204) return undefined as T;
    const payload = (await response.json().catch(() => null)) as
      | { error?: string; message?: string }
      | T
      | null;
    if (!response.ok) {
      const error = payload as { error?: string; message?: string; errorReason?: string } | null;
      if (response.status === 401 && typeof window !== "undefined") {
        window.dispatchEvent(new Event("robin:session-expired"));
      }
      throw new AppApiError(
        error?.message ?? commandErrorMessage(error?.errorReason) ?? "Application request failed.",
        response.status,
        error?.error ?? error?.errorReason ?? "request_failed",
        payload,
      );
    }
    return payload as T;
  }
}

function rejectedCommandMatches(
  value: unknown,
  agentId: string,
  command: AgentCommand,
  idempotencyKey: string,
) {
  if (!value || typeof value !== "object") return false;
  const record = value as Partial<AgentCommandRecord>;
  return record.status === "rejected"
    && record.agentId === agentId
    && record.command === command
    && record.idempotencyKey === idempotencyKey;
}

function commandErrorMessage(reason?: string) {
  if (reason === "external_execution_authority_requires_reconciliation") {
    return "Execution authority is already provisioned. Finish venue registration and reconciliation before closing; Robin will then require the owner-signed revocation.";
  }
  return reason?.replaceAll("_", " ");
}
