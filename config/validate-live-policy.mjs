#!/usr/bin/env node

import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const strategyDir = join(root, "config", "strategies");

const policyKeys = [
  "capitalActivationAllowed",
  "chainId",
  "executionEnabled",
  "limits",
  "requiredGates",
  "rolloutStage",
  "schemaVersion",
  "strategyManifestSha256",
  "strategyVersion",
];
const limitKeys = [
  "maxAccounts",
  "maxActiveEpisodesPerAccount",
  "maxAggregatePerVenueMicros",
  "maxDailyTurnoverMicros",
  "maxGrossNotionalMicros",
  "maxLegNotionalMicros",
  "maxLeveragePpm",
];
const gateKeys = [
  "alerting",
  "captureWindow",
  "contractAudit",
  "executorReview",
  "exitAuthority",
  "keyReview",
  "legalApproval",
  "oracleReview",
  "reconciliation",
  "restoreDrill",
  "safeOwnerRotation",
  "sequencerHealth",
  "shadowWindow",
  "venueApproval",
];
const manifestKeys = [
  "chain_id",
  "code_commit",
  "direction",
  "max_active_episodes",
  "max_daily_turnover_micros",
  "max_gross_notional_micros",
  "max_leverage_ppm",
  "max_leg_notional_micros",
  "oracle_policy_sha256",
  "risk_policy_sha256",
  "route_sha256",
  "schema_version",
  "sha256",
  "source_config_sha256",
  "strategy_version",
  "symbol",
];

const fixedLimits = {
  maxLegNotionalMicros: 25_000_000,
  maxGrossNotionalMicros: 50_000_000,
  maxDailyTurnoverMicros: 50_000_000,
  maxLeveragePpm: 1_000_000,
  maxActiveEpisodesPerAccount: 1,
};
const stageAllocations = {
  build: [[0, 0]],
  shadow: [[0, 0]],
  canary: [[1, 25_000_000]],
  cohort: [[5, 125_000_000], [25, 625_000_000]],
  public: [[100, 2_500_000_000]],
  retired: [[0, 0]],
};

export function calculateStrategyManifestHash(manifest) {
  const chunks = [u32(manifest.schema_version)];
  for (const value of [
    manifest.strategy_version,
    manifest.symbol,
    manifest.direction,
    manifest.source_config_sha256,
    manifest.route_sha256,
    manifest.oracle_policy_sha256,
    manifest.risk_policy_sha256,
    manifest.code_commit,
  ]) {
    const bytes = Buffer.from(value, "utf8");
    chunks.push(u64(bytes.length), bytes);
  }
  chunks.push(
    u64(manifest.chain_id),
    u64(manifest.max_leg_notional_micros),
    u64(manifest.max_gross_notional_micros),
    u64(manifest.max_daily_turnover_micros),
    u32(manifest.max_leverage_ppm),
    Buffer.from([manifest.max_active_episodes]),
  );
  return createHash("sha256").update(Buffer.concat(chunks)).digest("hex");
}

export function validateLivePolicy({
  policy,
  paper,
  render,
  personalVault,
  strategyManifest,
  strategyArtifacts,
}) {
  function fail(message) {
    throw new Error(`Invalid mainnet live policy: ${message}`);
  }

  exactKeys(policy, policyKeys, "policy", fail);
  exactKeys(policy.limits, limitKeys, "limits", fail);
  exactKeys(policy.requiredGates, gateKeys, "required gates", fail);
  validateManifest(strategyManifest, strategyArtifacts, fail);

  if (policy.schemaVersion !== 1) fail("unsupported schema version");
  if (policy.strategyVersion !== "basis-aapl-v1" || policy.chainId !== 4663) {
    fail("strategy identity must be basis-aapl-v1 on chain 4663");
  }
  if (policy.strategyManifestSha256 !== strategyManifest.sha256) {
    fail("strategy manifest checksum does not match the activation policy");
  }
  if (typeof policy.executionEnabled !== "boolean" || typeof policy.capitalActivationAllowed !== "boolean") {
    fail("activation flags must be boolean");
  }
  if (policy.executionEnabled !== policy.capitalActivationAllowed) {
    fail("execution and capital activation flags must match");
  }

  const limits = policy.limits;
  for (const [key, value] of Object.entries(fixedLimits)) {
    if (limits[key] !== value) fail(`${key} differs from the v1 strategy policy`);
  }
  for (const key of ["maxAccounts", "maxAggregatePerVenueMicros"]) {
    if (!Number.isSafeInteger(limits[key]) || limits[key] < 0) {
      fail(`${key} must be a non-negative safe integer`);
    }
  }
  const allocations = stageAllocations[policy.rolloutStage];
  if (!allocations) fail("unknown rollout stage");
  if (!allocations.some(([accounts, aggregate]) => (
    limits.maxAccounts === accounts && limits.maxAggregatePerVenueMicros === aggregate
  ))) {
    fail(`capital allocation is invalid for ${policy.rolloutStage}`);
  }

  const liveStage = ["canary", "cohort", "public"].includes(policy.rolloutStage);
  if (policy.executionEnabled !== liveStage) {
    fail(`activation flags are invalid for ${policy.rolloutStage}`);
  }
  const gates = Object.values(policy.requiredGates);
  if (gates.some((value) => typeof value !== "boolean")) {
    fail("required gates must be boolean");
  }
  if (liveStage && gates.some((value) => !value)) {
    fail("capital cannot be enabled with an open gate");
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
    const pattern = new RegExp(`key:\\s*${flag}\\s*\\n\\s*value:\\s*["']?(true|false)["']?`, "g");
    const matches = [...render.matchAll(pattern)];
    if (matches.length !== 1) fail(`${flag} must be declared exactly once`);
    if ((matches[0][1] === "true") !== policy.executionEnabled) {
      fail(`${flag} must match executionEnabled`);
    }
  }
}

function validateManifest(manifest, artifacts, fail) {
  if (!manifest || !artifacts) fail("strategy manifest inputs are missing");
  exactKeys(manifest, manifestKeys, "strategy manifest", fail);
  if (
    manifest.schema_version !== 1
    || manifest.strategy_version !== "basis-aapl-v1"
    || manifest.chain_id !== 4663
    || manifest.symbol !== "AAPL"
    || manifest.direction !== "long_spot_short_perp"
    || !/^[0-9a-f]{40}$/.test(manifest.code_commit)
  ) {
    fail("strategy manifest identity is invalid");
  }
  for (const key of [
    "source_config_sha256",
    "route_sha256",
    "oracle_policy_sha256",
    "risk_policy_sha256",
    "sha256",
  ]) {
    if (!/^[0-9a-f]{64}$/.test(manifest[key])) fail(`strategy manifest ${key} is invalid`);
  }
  if (
    manifest.max_leg_notional_micros !== fixedLimits.maxLegNotionalMicros
    || manifest.max_gross_notional_micros !== fixedLimits.maxGrossNotionalMicros
    || manifest.max_daily_turnover_micros !== fixedLimits.maxDailyTurnoverMicros
    || manifest.max_leverage_ppm !== fixedLimits.maxLeveragePpm
    || manifest.max_active_episodes !== fixedLimits.maxActiveEpisodesPerAccount
  ) {
    fail("strategy manifest limits differ from the v1 policy");
  }
  const expectedArtifacts = {
    source_config_sha256: sha256(artifacts.sourceConfig),
    route_sha256: sha256(artifacts.route),
    oracle_policy_sha256: sha256(artifacts.oraclePolicy),
    risk_policy_sha256: sha256(artifacts.riskPolicy),
  };
  for (const [key, expected] of Object.entries(expectedArtifacts)) {
    if (manifest[key] !== expected) fail(`strategy manifest ${key} does not match its artifact`);
  }
  if (manifest.sha256 !== calculateStrategyManifestHash(manifest)) {
    fail("strategy manifest checksum is invalid");
  }
}

function exactKeys(value, expected, label, fail) {
  if (!value || typeof value !== "object" || Array.isArray(value)) fail(`${label} must be an object`);
  const actual = Object.keys(value).sort();
  const required = [...expected].sort();
  if (actual.length !== required.length || actual.some((key, index) => key !== required[index])) {
    fail(`${label} keys must match the canonical schema`);
  }
}

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

function u32(value) {
  const output = Buffer.alloc(4);
  output.writeUInt32BE(value);
  return output;
}

function u64(value) {
  const output = Buffer.alloc(8);
  output.writeBigUInt64BE(BigInt(value));
  return output;
}

function readInputs() {
  return {
    policy: JSON.parse(readFileSync(join(root, "config", "mainnet-live-policy.json"), "utf8")),
    paper: JSON.parse(readFileSync(join(root, "runtime", "config", "mainnet-paper.json"), "utf8")),
    render: readFileSync(join(root, "render.yaml"), "utf8"),
    personalVault: readFileSync(join(root, "contracts", "src", "PersonalStrategyVault.sol"), "utf8"),
    strategyManifest: JSON.parse(readFileSync(join(strategyDir, "basis-aapl-v1.manifest.json"), "utf8")),
    strategyArtifacts: {
      sourceConfig: readFileSync(join(root, "runtime", "config", "mainnet-paper.json")),
      route: readFileSync(join(strategyDir, "basis-aapl-v1.route.json")),
      oraclePolicy: readFileSync(join(strategyDir, "basis-aapl-v1.oracle-policy.json")),
      riskPolicy: readFileSync(join(strategyDir, "basis-aapl-v1.risk-policy.json")),
    },
  };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  validateLivePolicy(readInputs());
  console.log("mainnet live policy is fail-closed");
}
