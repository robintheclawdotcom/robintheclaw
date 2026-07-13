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
  id: "vault-id", chainId: 46630, factoryVersion: 1,
  assetAddress: "0x5555555555555555555555555555555555555555",
  vaultAddress, guardAddress: "0x6666666666666666666666666666666666666666",
  anchorAddress: "0x7777777777777777777777777777777777777777",
  callId: `0x${"ab".repeat(32)}`, transactionHash: `0x${"cd".repeat(32)}`,
  status: "ready", createdAt: "2026-07-13T10:00:00Z", updatedAt: "2026-07-13T10:00:00Z",
};

export const agent = {
  id: "agent-id", strategyVersion: "basis-paper-v1", mode: "paper" as const,
  status: "running" as const, createdAt: "2026-07-13T10:00:00Z", updatedAt: "2026-07-13T10:00:00Z",
  evaluations: 42, candidates: 3, lastEvaluatedAt: "2026-07-13T10:04:00Z",
};

export const liveAgent = {
  ...agent,
  strategyVersion: "basis-aapl-v1",
  mode: "live" as const,
  status: "setup" as const,
  evaluations: 0,
  candidates: 0,
  lastEvaluatedAt: null,
};

export const readiness = {
  executionAccountId: "execution-account-id",
  robinhoodOwnerAddress: embedded,
  robinhoodVaultAddress: "0x8888888888888888888888888888888888888888",
  coordinatorRegistered: false,
  lighterLinked: false,
  lighterFunded: false,
  robinhoodDeployed: false,
  robinhoodFunded: false,
  userGasReady: false,
  executionGasReady: false,
  policyActive: false,
  reconciled: false,
  canLaunch: false,
  blockers: ["lighter_not_linked", "lighter_usdc_not_funded", "robinhood_vault_not_deployed"],
};

export function me(withVault = true) {
  return {
    user: { id: "user-id", privyDid: "did:privy:test-user", onboardingState: withVault ? "complete" : "vault", hasRecovery: true, createdAt: "2026-07-13T10:00:00Z", updatedAt: "2026-07-13T10:00:00Z" },
    wallets: [wallet(embedded, "embedded", true), wallet(external, "external", false)],
    smartAccount: { chainId: 46630, address: embedded, provider: "alchemy-eip7702", createdAt: "2026-07-13T10:00:00Z" },
    preferences: { displayCurrency: "USD", activeFundingWallet: external, notificationsEnabled: true, updatedAt: "2026-07-13T10:00:00Z" },
    vault: withVault ? vault : null,
  };
}

export const dashboard = {
  environment: "robinhood-testnet", asOf: "2026-07-13T10:05:00Z", infrastructureReady: true,
  agent,
  totalValue: { raw: "1000000000", decimals: 6, symbol: "tUSDG" },
  availableBalance: { raw: "0", decimals: 6, symbol: "tUSDG" },
  deployedCapital: { raw: "1000000000", decimals: 6, symbol: "tUSDG" }, pnl: null,
  smartAccount: me().smartAccount,
  vault: { record: vault, balance: { raw: "1000000000", decimals: 6, symbol: "tUSDG" }, halted: true, remainingCapacity: { raw: "5000000000", decimals: 6, symbol: "tUSDG" } },
  positions: [],
  opportunities: [{ symbol: "NVDA", basisBps: "42.5000", liquidity: "250000", observedAt: 1783936800 }],
  activity: [], wallets: me().wallets.map((item) => ({ wallet: item, balance: { raw: item.isPrimary ? "0" : "250000000", decimals: 6, symbol: "tUSDG" } })),
};

export async function mockApplication(page: Page, options: { withVault?: boolean; withAgent?: boolean } = {}) {
  const withVault = options.withVault ?? true;
  const state: { agent: object | null } = { agent: (options.withAgent ?? withVault) ? agent : null };
  await page.route("**/api/app/**", async (route) => respond(route, withVault, state));
}

async function respond(route: Route, withVault: boolean, state: { agent: object | null }) {
  const request = route.request();
  const path = new URL(request.url()).pathname;
  if (path.endsWith("/metrics")) return route.fulfill({ status: 204 });
  if (path.endsWith("/dashboard")) return json(route, { ...dashboard, agent: state.agent, vault: withVault ? dashboard.vault : null });
  if (path.endsWith("/agents") && request.method() === "POST") {
    state.agent = liveAgent;
    return json(route, liveAgent, 201);
  }
  if (path.endsWith("/execution-account") && request.method() === "POST") {
    state.agent = { ...liveAgent, status: "provisioning" };
    return json(route, { id: "execution-account-id", agentId: liveAgent.id, strategyVersion: "basis-aapl-v1", chainId: 4663, status: "provisioning", createdAt: liveAgent.createdAt, updatedAt: liveAgent.updatedAt }, 202);
  }
  if (path.endsWith("/readiness")) return json(route, readiness);
  if (path.endsWith("/lighter/link-request")) return json(route, { bindingRef: "lighter-binding", requestId: "lighter-request", venue: "lighter", ownerAddress: embedded, publicIdentifier: null, publicKey: null, associationPayload: null, proofTransactionHash: null, status: "provisioning", createdAt: liveAgent.createdAt, updatedAt: liveAgent.updatedAt }, 202);
  if (path.endsWith("/robinhood/prepare")) return json(route, {
    bindingRef: "robinhood-binding", requestId: "robinhood-request", providerRequestId: "execution-account-id",
    venue: "robinhood", ownerAddress: embedded, lighterAccountIndex: null, lighterApiKeyIndex: null,
    robinhoodVaultAddress: "0x8888888888888888888888888888888888888888",
    robinhoodSignerAddress: "0x9999999999999999999999999999999999999999", robinhoodKeyVersion: 1,
    robinhoodFactoryAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    robinhoodRegistryAddress: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    robinhoodPolicyDigest: `0x${"12".repeat(32)}`,
    robinhoodRiskManagerAddress: "0xcccccccccccccccccccccccccccccccccccccccc",
    robinhoodSpotAdapterAddress: "0xdddddddddddddddddddddddddddddddddddddddd",
    robinhoodDeploymentBlock: null,
    robinhoodDeploymentAction: {
      kind: "deploy_user_graph", chainId: "4663", to: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      data: `0x4c96a389${"0".repeat(24)}${embedded.slice(2)}`, value: "0",
    },
    publicIdentifier: null, publicKey: null, associationPayload: null, proofTransactionHash: null,
    status: "awaiting_signature", createdAt: liveAgent.createdAt, updatedAt: liveAgent.updatedAt,
  }, 202);
  if (path.endsWith("/robinhood/confirm")) return json(route, {
    bindingRef: "robinhood-binding", requestId: "robinhood-request", providerRequestId: "execution-account-id",
    venue: "robinhood", ownerAddress: embedded, lighterAccountIndex: null, lighterApiKeyIndex: null,
    robinhoodVaultAddress: "0x8888888888888888888888888888888888888888",
    robinhoodSignerAddress: "0x9999999999999999999999999999999999999999", robinhoodKeyVersion: 1,
    robinhoodFactoryAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    robinhoodRegistryAddress: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    robinhoodPolicyDigest: `0x${"12".repeat(32)}`,
    robinhoodRiskManagerAddress: "0xcccccccccccccccccccccccccccccccccccccccc",
    robinhoodSpotAdapterAddress: "0xdddddddddddddddddddddddddddddddddddddddd",
    robinhoodDeploymentBlock: 123, robinhoodDeploymentAction: null,
    publicIdentifier: null, publicKey: null, associationPayload: null,
    proofTransactionHash: `0x${"cd".repeat(32)}`, status: "linked",
    createdAt: liveAgent.createdAt, updatedAt: liveAgent.updatedAt,
  });
  if (path.includes("/agents/") && request.method() === "PUT") return json(route, agent);
  if (path.endsWith("/activity")) return json(route, { items: [], nextCursor: null });
  if (path.endsWith("/preferences")) return json(route, me(withVault).preferences);
  if (path.endsWith("/vaults/prepare")) return json(route, { chainId: 46630, smartAccount: embedded, expectedVault: vaultAddress, calls: [{ to: vault.assetAddress, data: "0x095ea7b3", value: "0" }] });
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
