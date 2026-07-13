import type {
  ActivityPage,
  AgentCommand,
  AgentCommandRecord,
  AgentReadiness,
  AgentRecord,
  DashboardSnapshot,
  ExecutionAccountRecord,
  ExecutionBindingRecord,
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
  ) {
    super(message);
  }
}

type TokenGetter = () => Promise<string | null>;

export class AppApi {
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

  launchPaperAgent(): Promise<AgentRecord> {
    return this.request("/v1/agents", { method: "POST" });
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

  requestLighterLink(agentId: string, ownerAddress: string): Promise<ExecutionBindingRecord> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/lighter/link-request`, {
      method: "POST",
      body: JSON.stringify({ ownerAddress }),
    });
  }

  confirmLighterLink(agentId: string, input: {
    requestId: string;
    ownerAddress: string;
    accountIndex: number;
    apiKeyIndex: number;
    associationTransactionHash: string;
  }): Promise<ExecutionBindingRecord> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/lighter/confirm`, {
      method: "POST",
      body: JSON.stringify(input),
    });
  }

  prepareRobinhood(agentId: string): Promise<ExecutionBindingRecord> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/robinhood/prepare`, { method: "POST" });
  }

  confirmRobinhood(agentId: string, input: {
    requestId: string;
    ownerAddress: string;
    vaultAddress: string;
    transactionHash: string;
  }): Promise<ExecutionBindingRecord> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/robinhood/confirm`, {
      method: "POST",
      body: JSON.stringify(input),
    });
  }

  agentCommand(agentId: string, command: AgentCommand, idempotencyKey = crypto.randomUUID()): Promise<AgentCommandRecord> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/commands`, {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey },
      body: JSON.stringify({ command }),
    });
  }

  getAgentCommand(agentId: string, commandId: string): Promise<AgentCommandRecord> {
    return this.request(`/v1/agents/${encodeURIComponent(agentId)}/commands/${encodeURIComponent(commandId)}`);
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
      const error = payload as { error?: string; message?: string } | null;
      if (response.status === 401 && typeof window !== "undefined") {
        window.dispatchEvent(new Event("robin:session-expired"));
      }
      throw new AppApiError(
        error?.message ?? "Application request failed.",
        response.status,
        error?.error ?? "request_failed",
      );
    }
    return payload as T;
  }
}
