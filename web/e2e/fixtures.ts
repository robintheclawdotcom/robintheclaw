import type { Page, Route } from "@playwright/test";

const embedded = "0x1111111111111111111111111111111111111111";
const external = "0x2222222222222222222222222222222222222222";
const vaultAddress = "0x4444444444444444444444444444444444444444";

export const wallet = (address: string, type: "embedded" | "external", primary: boolean) => ({
  id: `${type}-wallet`, chainNamespace: "eip155", address, walletType: type,
  label: type === "embedded" ? "Robin embedded wallet" : "MetaMask",
  isPrimary: primary, verifiedAt: "2026-07-13T10:00:00Z",
});

export const vault = {
  id: "vault-id", chainId: 4663, factoryVersion: 1,
  assetAddress: "0x5555555555555555555555555555555555555555",
  vaultAddress, guardAddress: "0x6666666666666666666666666666666666666666",
  anchorAddress: "0x7777777777777777777777777777777777777777",
  callId: `0x${"ab".repeat(32)}`, transactionHash: `0x${"cd".repeat(32)}`,
  status: "ready", createdAt: "2026-07-13T10:00:00Z", updatedAt: "2026-07-13T10:00:00Z",
};

export function me(withVault = true) {
  return {
    user: { id: "user-id", privyDid: "did:privy:test-user", onboardingState: withVault ? "complete" : "vault", hasRecovery: true, createdAt: "2026-07-13T10:00:00Z", updatedAt: "2026-07-13T10:00:00Z" },
    wallets: [wallet(embedded, "embedded", true), wallet(external, "external", false)],
    smartAccount: { chainId: 4663, address: embedded, provider: "alchemy-eip7702", createdAt: "2026-07-13T10:00:00Z" },
    preferences: { displayCurrency: "USD", activeFundingWallet: external, notificationsEnabled: true, updatedAt: "2026-07-13T10:00:00Z" },
    vault: withVault ? vault : null,
  };
}

export const dashboard = {
  environment: "robinhood-mainnet", asOf: "2026-07-13T10:05:00Z", infrastructureReady: true,
  totalValue: { raw: "1000000000", decimals: 6, symbol: "USDG" },
  availableBalance: { raw: "0", decimals: 6, symbol: "USDG" },
  deployedCapital: { raw: "1000000000", decimals: 6, symbol: "USDG" }, pnl: null,
  smartAccount: me().smartAccount,
  vault: { record: vault, balance: { raw: "1000000000", decimals: 6, symbol: "USDG" }, halted: true, remainingCapacity: { raw: "5000000000", decimals: 6, symbol: "USDG" } },
  positions: [],
  opportunities: [{ symbol: "NVDA", basisBps: "42.5000", liquidity: "250000", observedAt: 1783936800 }],
  activity: [], wallets: me().wallets.map((item) => ({ wallet: item, balance: { raw: item.isPrimary ? "0" : "250000000", decimals: 6, symbol: "USDG" } })),
};

export async function mockApplication(page: Page, options: { withVault?: boolean } = {}) {
  const withVault = options.withVault ?? true;
  await page.route("**/api/app/**", async (route) => respond(route, withVault));
}

async function respond(route: Route, withVault: boolean) {
  const request = route.request();
  const path = new URL(request.url()).pathname;
  if (path.endsWith("/metrics")) return route.fulfill({ status: 204 });
  if (path.endsWith("/dashboard")) return json(route, dashboard);
  if (path.endsWith("/activity")) return json(route, { items: [], nextCursor: null });
  if (path.endsWith("/preferences")) return json(route, me(withVault).preferences);
  if (path.endsWith("/vaults/prepare")) return json(route, { chainId: 4663, smartAccount: embedded, expectedVault: vaultAddress, calls: [{ to: vault.assetAddress, data: "0x095ea7b3", value: "0" }] });
  if (path.endsWith("/vaults/confirm")) return json(route, vault);
  if (path.endsWith("/wallets/sync")) {
    const synced = me(withVault);
    synced.wallets.push({ ...wallet("0x3333333333333333333333333333333333333333", "external", false), id: "phantom-wallet", label: "Phantom" });
    return json(route, synced);
  }
  if (path.endsWith("/me")) return json(route, me(withVault));
  return json(route, { error: "not_found", message: `No mock for ${path}` }, 404);
}

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}
