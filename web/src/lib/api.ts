import type {
  ActivityPage,
  DashboardSnapshot,
  MeResponse,
  PreferencesRecord,
  TransactionPlan,
  VaultRecord,
} from "./app-types";
import { robinhoodMainnetChainId } from "./chain";

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

  async me(): Promise<MeResponse> {
    return mainnetAccount(await this.request("/v1/me"));
  }

  async syncWallets(): Promise<MeResponse> {
    return mainnetAccount(await this.request("/v1/me/wallets/sync", { method: "POST" }));
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

  async dashboard(): Promise<DashboardSnapshot> {
    return mainnetDashboard(await this.request("/v1/dashboard"));
  }

  async activity(cursor?: string): Promise<ActivityPage> {
    const query = cursor ? `?cursor=${encodeURIComponent(cursor)}` : "";
    const page = await this.request<ActivityPage>(`/v1/activity${query}`);
    return { ...page, items: page.items.filter((item) => item.chainId === robinhoodMainnetChainId) };
  }

  prepareVault(): Promise<TransactionPlan> {
    return this.request("/v1/vaults/prepare", { method: "POST" });
  }

  async confirmVault(callId: string): Promise<VaultRecord> {
    const vault = await this.request<VaultRecord>("/v1/vaults/confirm", {
      method: "POST",
      body: JSON.stringify({ callId }),
    });
    if (vault.chainId !== robinhoodMainnetChainId) {
      throw new AppApiError("The confirmed vault is not a Robinhood Chain mainnet deployment.", 409, "wrong_chain");
    }
    return vault;
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

function mainnetAccount(account: MeResponse): MeResponse {
  return {
    ...account,
    smartAccount: account.smartAccount?.chainId === robinhoodMainnetChainId ? account.smartAccount : null,
    vault: account.vault?.chainId === robinhoodMainnetChainId ? account.vault : null,
  };
}

function mainnetDashboard(dashboard: DashboardSnapshot): DashboardSnapshot {
  const smartAccount = dashboard.smartAccount?.chainId === robinhoodMainnetChainId
    ? dashboard.smartAccount
    : null;
  const vault = dashboard.vault?.record.chainId === robinhoodMainnetChainId
    ? dashboard.vault
    : null;
  if (dashboard.environment === "robinhood-mainnet" && smartAccount === dashboard.smartAccount && vault === dashboard.vault) {
    return dashboard;
  }
  const zero = { raw: "0", decimals: 6, symbol: "USDG" };
  return {
    ...dashboard,
    environment: "robinhood-mainnet",
    infrastructureReady: false,
    totalValue: zero,
    availableBalance: zero,
    deployedCapital: zero,
    pnl: null,
    smartAccount,
    vault,
    positions: vault ? dashboard.positions : [],
    activity: dashboard.activity.filter((item) => item.chainId === robinhoodMainnetChainId),
    wallets: dashboard.wallets.map(({ wallet }) => ({ wallet, balance: zero })),
  };
}
