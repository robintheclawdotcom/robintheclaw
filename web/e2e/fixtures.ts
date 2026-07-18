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
  lighterAccountIndex: null,
  robinhoodOwnerAddress: embedded,
  robinhoodVaultAddress: "0x8888888888888888888888888888888888888888",
  robinhoodSignerAddress: "0x9999999999999999999999999999999999999999",
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
    smartAccount: { chainId: 4663, address: embedded, provider: "privy", createdAt: "2026-07-13T10:00:00Z" },
    preferences: { displayCurrency: "USD", activeFundingWallet: external, notificationsEnabled: true, updatedAt: "2026-07-13T10:00:00Z" },
    vault: withVault ? vault : null,
  };
}

export const dashboard = {
  environment: "robinhood-mainnet", asOf: "2026-07-13T10:05:00Z", infrastructureReady: true,
  agent,
  totalValue: { raw: "1000000000", decimals: 6, symbol: "USDG" },
  availableBalance: { raw: "0", decimals: 6, symbol: "USDG" },
  deployedCapital: { raw: "1000000000", decimals: 6, symbol: "USDG" }, pnl: null,
  smartAccount: me().smartAccount,
  vault: { record: vault, balance: { raw: "1000000000", decimals: 6, symbol: "USDG" }, halted: true, remainingCapacity: { raw: "5000000000", decimals: 6, symbol: "USDG" } },
  positions: [],
  opportunities: [{ symbol: "AAPL", basisBps: "42.5000", liquidity: "250000", observedAt: 1783936800 }],
  activity: [], wallets: me().wallets.map((item) => ({ wallet: item, balance: { raw: item.isPrimary ? "0" : "250000000", decimals: 6, symbol: "USDG" } })),
};

export async function mockApplication(page: Page, options: { withVault?: boolean; withAgent?: boolean; liveJourney?: boolean } = {}) {
  const withVault = options.withVault ?? true;
  const state: MockState = {
    agent: (options.withAgent ?? withVault) ? agent : null,
    robinhoodConfirmations: 0,
    lighterLinked: false,
    robinhoodLinked: false,
    liveJourney: options.liveJourney ?? false,
    command: null,
    executed: false,
    lighterFunded: false,
    robinhoodFunded: false,
    executionGasReady: false,
    pauseReconciled: false,
    closeReconciled: false,
    lighterRevocationStatus: null,
    lighterRevocationSubstituted: false,
    withdrawalReconciled: false,
  };
  await page.route("**/api/app/**", async (route) => respond(route, withVault, state));
  return {
    observeLighterFunding: () => { state.lighterFunded = true; },
    observeRobinhoodFunding: () => { state.robinhoodFunded = true; },
    observeExecutionGas: () => { state.executionGasReady = true; },
    reconcilePause: () => { state.pauseReconciled = true; },
    reconcileClose: () => {
      state.closeReconciled = true;
      state.lighterRevocationStatus = "pending";
    },
    substituteLighterRevocation: () => { state.lighterRevocationSubstituted = true; },
    restoreLighterRevocation: () => { state.lighterRevocationSubstituted = false; },
    reconcileWithdrawal: () => { state.withdrawalReconciled = true; },
  };
}

type MockState = {
  agent: object | null;
  robinhoodConfirmations: number;
  lighterLinked: boolean;
  robinhoodLinked: boolean;
  liveJourney: boolean;
  command: Record<string, unknown> | null;
  executed: boolean;
  lighterFunded: boolean;
  robinhoodFunded: boolean;
  executionGasReady: boolean;
  pauseReconciled: boolean;
  closeReconciled: boolean;
  lighterRevocationStatus: "pending" | "revoked" | null;
  lighterRevocationSubstituted: boolean;
  withdrawalReconciled: boolean;
};

async function respond(route: Route, withVault: boolean, state: MockState) {
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
  if (path.endsWith("/readiness")) {
    const registered = state.liveJourney && state.lighterLinked && state.robinhoodLinked;
    const ready = registered && state.lighterFunded && state.robinhoodFunded && state.executionGasReady;
    const status = (state.agent as { status?: string } | null)?.status;
    if (ready && matchesSetupStatus(status)) state.agent = { ...liveAgent, status: "ready" };
    return json(route, registered ? {
      ...readiness,
      lighterAccountIndex: 42,
      coordinatorRegistered: true,
      lighterLinked: true,
      lighterFunded: state.lighterFunded,
      robinhoodDeployed: true,
      robinhoodFunded: state.robinhoodFunded,
      userGasReady: true,
      executionGasReady: state.executionGasReady,
      policyActive: true,
      reconciled: true,
      canLaunch: ready,
      validUntil: "2099-01-01T00:00:00Z",
      blockers: [
        !state.lighterFunded && "lighter_usdc_not_funded",
        !state.robinhoodFunded && "robinhood_vault_not_funded",
        !state.executionGasReady && "execution_signer_gas_not_ready",
      ].filter(Boolean),
    } : readiness);
  }
  if (path.endsWith("/execution") && request.method() === "GET") {
    const status = (state.agent as { status?: string } | null)?.status;
    const running = state.executed && status === "running";
    const reducing = state.executed && status === "reducing";
    const active = running || reducing;
    const hasEpisode = state.executed;
    return json(route, {
      executionAccountId: "execution-account-id",
      agentId: liveAgent.id,
      strategyVersion: "basis-aapl-v1",
      strategyManifestSha256: "7787f323c898f08bec51028ced5ee402f18f85da891515306ee330b2171c3902",
      accountStatus: status === "closed" ? "closed" : "active",
      controlMode: active ? "ACTIVE" : status === "closed" ? "HALTED" : "REDUCE_ONLY",
      active,
      flat: hasEpisode ? !active : true,
      intentId: hasEpisode ? `0x${"ab".repeat(32)}` : null,
      symbol: hasEpisode ? "AAPL" : null,
      state: running ? "hedged" : reducing ? "unwinding" : hasEpisode ? "closed" : "flat",
      spotAmountRaw: active ? "100000000000000000" : "0",
      spotDecimals: active ? 18 : 0,
      perpOpenBase: active ? "100" : "0",
      perpBaseDecimals: active ? 3 : 0,
      spotNotionalMicros: hasEpisode ? "25000000" : "0",
      perpNotionalMicros: hasEpisode ? "25000000" : "0",
      lighterOrderId: hasEpisode ? "lighter-order-123" : null,
      lighterTransactionHash: hasEpisode ? `0x${"12".repeat(32)}` : null,
      robinhoodTransactionHash: hasEpisode ? `0x${"34".repeat(32)}` : null,
      lighterUnwindOrderId: hasEpisode && !active ? "lighter-unwind-456" : null,
      lighterUnwindTransactionHash: hasEpisode && !active ? `0x${"56".repeat(32)}` : null,
      robinhoodUnwindTransactionHash: hasEpisode && !active ? `0x${"78".repeat(32)}` : null,
      updatedAtMs: hasEpisode ? Date.parse(liveAgent.updatedAt) : 0,
    });
  }
  if (path.endsWith("/lighter/link-request")) {
    const body = request.postDataJSON() as Record<string, unknown>;
    if (JSON.stringify(body) !== JSON.stringify({ ownerAddress: embedded })) {
      return json(route, { error: "invalid_request", message: "Only ownerAddress is accepted." }, 400);
    }
    return json(route, {
      bindingRef: "lighter-binding", requestId: "lighter-request",
      providerRequestId: "11111111-1111-4111-8111-111111111111", venue: "lighter",
      ownerAddress: embedded, lighterAccountIndex: 42, lighterApiKeyIndex: 254,
      robinhoodVaultAddress: null, robinhoodSignerAddress: null, robinhoodKeyVersion: null,
      robinhoodFactoryAddress: null, robinhoodRegistryAddress: null, robinhoodPolicyDigest: null,
      robinhoodRiskManagerAddress: null, robinhoodSpotAdapterAddress: null,
      robinhoodDeploymentBlock: null, robinhoodDeploymentAction: null,
      robinhoodAuthorizationTransactionHash: null, robinhoodAuthorizationBlock: null,
      publicIdentifier: "account:42:key:254", publicKey: `0x${"12".repeat(40)}`,
      associationPayload: "Register Lighter Account\nfixture", proofTransactionHash: null,
      status: "awaiting_signature", createdAt: liveAgent.createdAt, updatedAt: liveAgent.updatedAt,
    }, 201);
  }
  if (path.endsWith("/lighter/confirm")) {
    state.lighterLinked = true;
    return json(route, {
    bindingRef: "lighter-binding", requestId: "lighter-request",
    providerRequestId: "11111111-1111-4111-8111-111111111111", venue: "lighter",
    ownerAddress: embedded, lighterAccountIndex: 42, lighterApiKeyIndex: 254,
    robinhoodVaultAddress: null, robinhoodSignerAddress: null, robinhoodKeyVersion: null,
    robinhoodFactoryAddress: null, robinhoodRegistryAddress: null, robinhoodPolicyDigest: null,
    robinhoodRiskManagerAddress: null, robinhoodSpotAdapterAddress: null,
    robinhoodDeploymentBlock: null, robinhoodDeploymentAction: null,
    robinhoodAuthorizationTransactionHash: null, robinhoodAuthorizationBlock: null,
    publicIdentifier: "account:42:key:254", publicKey: `0x${"12".repeat(40)}`,
    associationPayload: "Register Lighter Account\nfixture", proofTransactionHash: `0x${"34".repeat(32)}`,
    status: "linked", createdAt: liveAgent.createdAt, updatedAt: liveAgent.updatedAt,
    });
  }
  if (path.endsWith("/lighter/revocation") && request.method() === "GET") {
    if (!state.closeReconciled || !state.lighterRevocationStatus) {
      return json(route, { error: "invalid_request", message: "Lighter revocation is not ready." }, 400);
    }
    if (state.lighterRevocationSubstituted) {
      return json(route, {
        error: "service_unavailable",
        message: "Lighter revocation binding does not match the canonical owner, account, or key.",
      }, 503);
    }
    return json(route, lighterRevocation(state.lighterRevocationStatus));
  }
  if (path.endsWith("/lighter/revocation/confirm") && request.method() === "POST") {
    const input = request.postDataJSON() as Record<string, unknown>;
    if (
      state.lighterRevocationSubstituted
      || Object.keys(input).sort().join(",") !== "l1Signature,revocationId"
      || input.revocationId !== "lighter-revocation"
      || input.l1Signature !== `0x${"11".repeat(65)}`
    ) {
      return json(route, {
        error: "conflict",
        message: "Lighter revocation proof does not match the canonical binding.",
      }, 409);
    }
    state.lighterRevocationStatus = "revoked";
    return json(route, lighterRevocation("revoked"));
  }
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
    robinhoodAuthorizationTransactionHash: null, robinhoodAuthorizationBlock: null,
    robinhoodDeploymentAction: {
      kind: "deploy_user_graph", chainId: "4663", to: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      data: `0x4c96a389${"0".repeat(24)}${embedded.slice(2)}`, value: "0",
    },
    publicIdentifier: null, publicKey: null, associationPayload: null, proofTransactionHash: null,
    status: "awaiting_signature", createdAt: liveAgent.createdAt, updatedAt: liveAgent.updatedAt,
  }, 202);
  if (path.endsWith("/robinhood/confirm")) {
    const input = request.postDataJSON() as { transactionHash?: string };
    state.robinhoodConfirmations += 1;
    const authorized = state.robinhoodConfirmations > 1;
    if (!authorized && input.transactionHash !== `0x${"cd".repeat(32)}`) {
      return json(route, { error: "invalid_proof", message: "Deployment confirmation requires the deployment transaction." }, 409);
    }
    if (authorized && input.transactionHash !== `0x${"ef".repeat(32)}`) {
      return json(route, { error: "invalid_proof", message: "Authorization confirmation requires the authorization transaction." }, 409);
    }
    if (authorized) {
      state.robinhoodLinked = true;
      if (state.liveJourney && state.lighterLinked) state.agent = { ...liveAgent, status: "awaiting_funding" };
    }
    return json(route, {
    bindingRef: "robinhood-binding", requestId: "robinhood-request", providerRequestId: "execution-account-id",
    venue: "robinhood", ownerAddress: embedded, lighterAccountIndex: null, lighterApiKeyIndex: null,
    robinhoodVaultAddress: "0x8888888888888888888888888888888888888888",
    robinhoodSignerAddress: "0x9999999999999999999999999999999999999999", robinhoodKeyVersion: 1,
    robinhoodFactoryAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    robinhoodRegistryAddress: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    robinhoodPolicyDigest: `0x${"12".repeat(32)}`,
    robinhoodRiskManagerAddress: "0xcccccccccccccccccccccccccccccccccccccccc",
    robinhoodSpotAdapterAddress: "0xdddddddddddddddddddddddddddddddddddddddd",
    robinhoodDeploymentBlock: 123,
    robinhoodDeploymentAction: authorized ? null : {
      kind: "authorize_execution_agent", chainId: "4663",
      to: "0x8888888888888888888888888888888888888888",
      data: `0xa7d1c2a0${"0".repeat(24)}${"9999999999999999999999999999999999999999"}`, value: "0",
    },
    robinhoodAuthorizationTransactionHash: authorized ? `0x${"ef".repeat(32)}` : null,
    robinhoodAuthorizationBlock: authorized ? 124 : null,
    publicIdentifier: null, publicKey: null, associationPayload: null,
    proofTransactionHash: `0x${"cd".repeat(32)}`, status: authorized ? "linked" : "awaiting_signature",
    createdAt: liveAgent.createdAt, updatedAt: liveAgent.updatedAt,
  });
  }
  if (path.endsWith("/commands") && request.method() === "POST") {
    const { command } = request.postDataJSON() as { command: "launch" | "pause" | "resume" | "close" | "withdraw" };
    const coordinatorRegistered = state.liveJourney && state.lighterLinked && state.robinhoodLinked;
    const externalAuthorityProvisioned = state.lighterLinked || state.robinhoodConfirmations > 0;
    if (command === "close" && externalAuthorityProvisioned && !coordinatorRegistered) {
      state.command = {
        id: "command-close-rejected", agentId: liveAgent.id, executionAccountId: "execution-account-id",
        idempotencyKey: request.headers()["idempotency-key"] ?? "fixture-key", command,
        status: "rejected", agentStatus: (state.agent as { status?: string } | null)?.status ?? "provisioning",
        targetAgentStatus: "closed",
        errorReason: "external_execution_authority_requires_reconciliation",
        resultEvidenceDigest: null, ownerActions: [], completedAt: null,
        createdAt: liveAgent.createdAt, updatedAt: liveAgent.updatedAt,
      };
      return json(route, state.command, 409);
    }
    const closeNeedsRevocation = command === "close" && coordinatorRegistered;
    const nextStatus = command === "launch" || command === "resume" ? "running"
      : command === "pause" ? "reducing"
        : command === "close" ? closeNeedsRevocation ? "closing" : "closed"
          : command === "withdraw" ? "closed" : "blocked";
    if (command !== "withdraw") state.agent = { ...liveAgent, status: nextStatus };
    if (command === "launch" || command === "resume") state.executed = true;
    state.command = {
      id: `command-${command}`, agentId: liveAgent.id, executionAccountId: "execution-account-id",
      idempotencyKey: request.headers()["idempotency-key"] ?? "fixture-key", command,
      status: command === "withdraw" || closeNeedsRevocation ? "awaiting_signature" : command === "pause" ? "processing" : "completed",
      agentStatus: nextStatus, targetAgentStatus: command === "close" ? "closed" : nextStatus, errorReason: null,
      resultEvidenceDigest: command === "withdraw" ? null : `0x${"45".repeat(32)}`,
      ownerActions: command === "withdraw" ? [{
        chain_id: 4663, from: embedded, to: readiness.robinhoodVaultAddress,
        data: `0x142834dd${"0".repeat(63)}1`, value: "0",
      }] : closeNeedsRevocation ? [{
        chain_id: 4663, from: embedded, to: readiness.robinhoodVaultAddress,
        data: "0x51755334", value: "0",
      }] : [],
      completedAt: command === "withdraw" || closeNeedsRevocation || command === "pause" ? null : liveAgent.updatedAt,
      createdAt: liveAgent.createdAt, updatedAt: liveAgent.updatedAt,
    };
    return json(route, state.command, command === "withdraw" || closeNeedsRevocation ? 202 : 200);
  }
  if (path.endsWith("/commands/pending") && request.method() === "GET") {
    const active = state.command
      && typeof state.command.status === "string"
      && !["completed", "rejected", "failed"].includes(state.command.status)
      ? state.command
      : null;
    return json(route, active);
  }
  if (path.includes("/commands/") && request.method() === "GET" && state.command) {
    if (state.pauseReconciled && state.command.command === "pause") {
      state.agent = { ...liveAgent, status: "paused" };
      state.command = {
        ...state.command,
        status: "completed",
        agentStatus: "paused",
        targetAgentStatus: "paused",
        completedAt: liveAgent.updatedAt,
      };
    }
    if (
      state.closeReconciled
      && state.lighterRevocationStatus === "revoked"
      && state.command.command === "close"
    ) {
      state.agent = { ...liveAgent, status: "closed" };
      state.command = {
        ...state.command,
        status: "completed",
        agentStatus: "closed",
        targetAgentStatus: "closed",
        ownerActions: [],
        completedAt: liveAgent.updatedAt,
      };
    }
    if (state.withdrawalReconciled && state.command.command === "withdraw") {
      state.command = {
        ...state.command,
        status: "completed",
        resultEvidenceDigest: `0x${"67".repeat(32)}`,
        ownerActions: [],
        completedAt: liveAgent.updatedAt,
      };
    }
    return json(route, state.command);
  }
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

function lighterRevocation(status: "pending" | "revoked") {
  return {
    revocationId: "lighter-revocation",
    executionAccountId: "execution-account-id",
    ownerAddress: embedded,
    accountIndex: 42,
    apiKeyIndex: 254,
    tombstonePublicKey: `0x${"98".repeat(40)}`,
    status,
    messageToSign: status === "pending" ? "Revoke Lighter execution key\nfixture" : null,
    transactionHash: status === "revoked" ? `0x${"76".repeat(32)}` : null,
    registeredPublicKey: status === "revoked" ? "98".repeat(40) : null,
    createdAt: liveAgent.createdAt,
    updatedAt: liveAgent.updatedAt,
  };
}

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}

function matchesSetupStatus(status?: string) {
  return status === "provisioning"
    || status === "awaiting_signatures"
    || status === "awaiting_funding"
    || status === "ready";
}
