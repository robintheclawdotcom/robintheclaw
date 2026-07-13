#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

export function validateLivePolicy({ policy, paper, render, personalVault }) {
  function fail(message) {
    throw new Error(`Invalid mainnet live policy: ${message}`);
  }

  if (policy.schemaVersion !== 1) fail("unsupported schema version");
  if (policy.strategyVersion !== "basis-aapl-v1" || policy.chainId !== 4663) {
    fail("strategy identity must be basis-aapl-v1 on chain 4663");
  }
  if (!["build", "shadow", "canary", "cohort", "public", "retired"].includes(policy.rolloutStage)) {
    fail("unknown rollout stage");
  }

  const limits = policy.limits ?? {};
  const fixedLimits = {
    maxLegNotionalMicros: 25_000_000,
    maxGrossNotionalMicros: 50_000_000,
    maxDailyTurnoverMicros: 50_000_000,
    maxLeveragePpm: 1_000_000,
    maxActiveEpisodesPerAccount: 1,
  };
  for (const [key, value] of Object.entries(fixedLimits)) {
    if (limits[key] !== value) fail(`${key} differs from the v1 strategy policy`);
  }
  if (!Number.isSafeInteger(limits.maxAccounts) || limits.maxAccounts < 0) {
    fail("maxAccounts must be a non-negative integer");
  }
  if (!Number.isSafeInteger(limits.maxAggregatePerVenueMicros) || limits.maxAggregatePerVenueMicros < 0) {
    fail("maxAggregatePerVenueMicros must be a non-negative integer");
  }
  if (limits.maxAggregatePerVenueMicros > limits.maxAccounts * limits.maxLegNotionalMicros) {
    fail("aggregate venue cap exceeds the account allocation");
  }

  const gates = Object.values(policy.requiredGates ?? {});
  if (gates.length !== 14 || gates.some((value) => typeof value !== "boolean")) {
    fail("required gates are incomplete");
  }
  if (policy.executionEnabled || policy.capitalActivationAllowed) {
    if (!["canary", "cohort", "public"].includes(policy.rolloutStage)) {
      fail("capital cannot be enabled outside a live rollout stage");
    }
    if (gates.some((value) => !value)) fail("capital cannot be enabled with an open gate");
    if (limits.maxAccounts < 1 || limits.maxAggregatePerVenueMicros < 25_000_000) {
      fail("capital cannot be enabled without a bounded account allocation");
    }
  } else if (limits.maxAccounts !== 0 || limits.maxAggregatePerVenueMicros !== 0) {
    fail("disabled execution must not reserve a capital allocation");
  }

  if (paper.chainId !== policy.chainId || paper.markets?.length !== 1 || paper.markets[0]?.symbol !== "AAPL") {
    fail("paper source configuration is not the AAPL mainnet strategy");
  }
  if (paper.markets[0].amountInRaw !== String(limits.maxLegNotionalMicros)) {
    fail("paper source notional differs from the live policy");
  }
  if (!/SUPPORTED_CHAIN_ID\s*=\s*46630\b/.test(personalVault)) {
    fail("generic personal vault is no longer locked to testnet");
  }
  for (const flag of ["COORDINATOR_ENABLED", "LIGHTER_SIGNER_ENABLED", "ROBINHOOD_SIGNER_ENABLED"]) {
    const pattern = new RegExp(`key:\\s*${flag}\\s*\\n\\s*value:\\s*["']?false["']?`);
    if (!pattern.test(render)) fail(`${flag} must remain false in the deployment blueprint`);
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  validateLivePolicy({
    policy: JSON.parse(readFileSync(join(root, "config", "mainnet-live-policy.json"), "utf8")),
    paper: JSON.parse(readFileSync(join(root, "runtime", "config", "mainnet-paper.json"), "utf8")),
    render: readFileSync(join(root, "render.yaml"), "utf8"),
    personalVault: readFileSync(join(root, "contracts", "src", "PersonalStrategyVault.sol"), "utf8"),
  });
  console.log("mainnet live policy is fail-closed");
}
