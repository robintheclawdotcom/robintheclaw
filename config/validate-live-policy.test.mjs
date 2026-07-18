import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { validateLivePolicy } from "./validate-live-policy.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const strategyDir = join(root, "config", "strategies");
const executionFlags = [
  "ACCOUNT_PUBLISHER_ENABLED",
  "COORDINATOR_ENABLED",
  "LIGHTER_PROVISIONER_ENABLED",
  "LIGHTER_SIGNER_ENABLED",
  "ROBINHOOD_PROVISIONER_ENABLED",
  "ROBINHOOD_SIGNER_ENABLED",
  "ROBIN_QUOTE_AUTHORITY_ENABLED",
  "ROBIN_STRATEGY_RUNNER_ENABLED",
  "ROBIN_LIVE_SCHEDULER_ENABLED",
  "ROBIN_LIVE_EVALUATION_ENABLED",
  "ROBIN_EXIT_QUOTE_PUBLISHER_ENABLED",
];

function fixture() {
  return {
    policy: JSON.parse(readFileSync(join(root, "config", "mainnet-live-policy.json"), "utf8")),
    paper: JSON.parse(readFileSync(join(root, "runtime", "config", "mainnet-paper.json"), "utf8")),
    render: readFileSync(join(root, "render.yaml"), "utf8"),
    personalVault: readFileSync(join(root, "contracts", "src", "PersonalStrategyVault.sol"), "utf8"),
    appProduct: readFileSync(join(root, "app", "src", "product.rs"), "utf8"),
    strategyManifest: JSON.parse(readFileSync(join(strategyDir, "basis-aapl-v1.manifest.json"), "utf8")),
    strategyArtifacts: {
      sourceConfig: readFileSync(join(root, "runtime", "config", "mainnet-paper.json")),
      route: readFileSync(join(strategyDir, "basis-aapl-v1.route.json")),
      oraclePolicy: readFileSync(join(strategyDir, "basis-aapl-v1.oracle-policy.json")),
      riskPolicy: readFileSync(join(strategyDir, "basis-aapl-v1.risk-policy.json")),
      policyConsumers: {
        liveEvaluation: readFileSync(join(root, "runtime", "live-evaluation", "strategy_policy.go"), "utf8"),
        paperAgent: readFileSync(join(root, "runtime", "src", "paper.rs"), "utf8"),
      },
    },
  };
}

function enable(input, stage, accounts, aggregate) {
  input.policy.rolloutStage = stage;
  input.policy.executionEnabled = true;
  input.policy.capitalActivationAllowed = true;
  input.policy.limits.maxAccounts = accounts;
  input.policy.limits.maxAggregatePerVenueMicros = aggregate;
  for (const gate of Object.keys(input.policy.requiredGates)) input.policy.requiredGates[gate] = true;
  for (const flag of executionFlags) {
    const pattern = new RegExp(`(key:\\s*${flag}\\s*\\n\\s*value:\\s*["']?)false(["']?)`);
    input.render = input.render.replace(pattern, "$1true$2");
  }
}

test("accepts the canonical capped canary policy", () => {
  assert.doesNotThrow(() => validateLivePolicy(fixture()));
});

test("accepts only the canonical allocations for live stages", () => {
  for (const [stage, accounts, aggregate] of [
    ["canary", 1, 25_000_000],
    ["cohort", 5, 125_000_000],
    ["cohort", 25, 625_000_000],
    ["public", 100, 2_500_000_000],
  ]) {
    const input = fixture();
    enable(input, stage, accounts, aggregate);
    assert.doesNotThrow(() => validateLivePolicy(input), stage);
  }
});

test("rejects a missing required gate even when every remaining value is true", () => {
  const input = fixture();
  delete input.policy.requiredGates.internalAudit;
  assert.throws(() => validateLivePolicy(input), /required gates keys/);
});

test("rejects an unknown gate that replaces a required gate", () => {
  const input = fixture();
  delete input.policy.requiredGates.internalAudit;
  input.policy.requiredGates.notARealGate = true;
  assert.throws(() => validateLivePolicy(input), /required gates keys/);
});

test("rejects non-boolean gates and activation flags", () => {
  const gateInput = fixture();
  gateInput.policy.requiredGates.internalAudit = "false";
  assert.throws(() => validateLivePolicy(gateInput), /required gates must be boolean/);

  const flagInput = fixture();
  flagInput.policy.executionEnabled = "false";
  flagInput.policy.capitalActivationAllowed = "false";
  assert.throws(() => validateLivePolicy(flagInput), /activation flags must be boolean/);
});

test("rejects unknown policy and limit fields", () => {
  const policyInput = fixture();
  policyInput.policy.override = true;
  assert.throws(() => validateLivePolicy(policyInput), /policy keys/);

  const limitInput = fixture();
  limitInput.policy.limits.emergencyCap = 1;
  assert.throws(() => validateLivePolicy(limitInput), /limits keys/);
});

test("rejects activation with an open gate", () => {
  const input = fixture();
  enable(input, "canary", 1, 25_000_000);
  input.policy.requiredGates.oracleReview = false;
  assert.throws(() => validateLivePolicy(input), /open gate/);
});

test("rejects incongruent execution and capital flags", () => {
  const input = fixture();
  input.policy.capitalActivationAllowed = false;
  assert.throws(() => validateLivePolicy(input), /activation flags must match/);
});

test("rejects activation flags that disagree with the rollout stage", () => {
  const input = fixture();
  input.policy.rolloutStage = "build";
  input.policy.limits.maxAccounts = 0;
  input.policy.limits.maxAggregatePerVenueMicros = 0;
  assert.throws(() => validateLivePolicy(input), /activation flags are invalid for build/);
});

test("rejects non-canonical canary and cohort allocations", () => {
  const canary = fixture();
  enable(canary, "canary", 2, 50_000_000);
  assert.throws(() => validateLivePolicy(canary), /allocation is invalid for canary/);

  const cohort = fixture();
  enable(cohort, "cohort", 10, 250_000_000);
  assert.throws(() => validateLivePolicy(cohort), /allocation is invalid for cohort/);
});

test("rejects a raised fixed strategy cap", () => {
  const input = fixture();
  input.policy.limits.maxLegNotionalMicros += 1;
  assert.throws(() => validateLivePolicy(input), /differs from the v1 strategy policy/);
});

test("requires every blueprint execution flag to match policy activation", () => {
  const disabled = fixture();
  disabled.render = disabled.render.replace(
    /(key:\s*LIGHTER_SIGNER_ENABLED\s*\n\s*value:\s*["']?)true(["']?)/,
    "$1false$2",
  );
  assert.throws(() => validateLivePolicy(disabled), /LIGHTER_SIGNER_ENABLED must match/);

  const schedulerDisabled = fixture();
  schedulerDisabled.render = schedulerDisabled.render.replace(
    /(key:\s*ROBIN_LIVE_SCHEDULER_ENABLED\s*\n\s*value:\s*["']?)true(["']?)/,
    "$1false$2",
  );
  assert.throws(() => validateLivePolicy(schedulerDisabled), /ROBIN_LIVE_SCHEDULER_ENABLED must match/);

  for (const flag of ["ROBIN_LIVE_EVALUATION_ENABLED", "ROBIN_EXIT_QUOTE_PUBLISHER_ENABLED"]) {
    const workerDisabled = fixture();
    workerDisabled.render = workerDisabled.render.replace(
      new RegExp(`(key:\\s*${flag}\\s*\\n\\s*value:\\s*["']?)true(["']?)`),
      "$1false$2",
    );
    assert.throws(() => validateLivePolicy(workerDisabled), new RegExp(`${flag} must match`));
  }

  const enabled = fixture();
  enable(enabled, "canary", 1, 25_000_000);
  enabled.render = enabled.render.replace(
    /(key:\s*ROBINHOOD_SIGNER_ENABLED\s*\n\s*value:\s*["']?)true(["']?)/,
    "$1false$2",
  );
  assert.throws(() => validateLivePolicy(enabled), /ROBINHOOD_SIGNER_ENABLED must match/);
});

test("rejects policy and manifest checksum mismatch", () => {
  const input = fixture();
  input.policy.strategyManifestSha256 = "0".repeat(64);
  assert.throws(() => validateLivePolicy(input), /manifest checksum does not match/);
});

test("rejects an application account manifest mismatch", () => {
  const input = fixture();
  input.appProduct = input.appProduct.replace(
    input.strategyManifest.sha256,
    "0".repeat(64),
  );
  assert.throws(() => validateLivePolicy(input), /application execution accounts/);
});

test("rejects a tampered manifest even if the policy checksum is also changed", () => {
  const input = fixture();
  input.strategyManifest.route_sha256 = "0".repeat(64);
  input.policy.strategyManifestSha256 = input.strategyManifest.sha256;
  assert.throws(() => validateLivePolicy(input), /route_sha256 does not match its artifact/);
});

test("rejects a changed source artifact", () => {
  const input = fixture();
  input.strategyArtifacts.riskPolicy = Buffer.from("{}\n");
  assert.throws(() => validateLivePolicy(input), /risk_policy_sha256 does not match its artifact/);
});

test("rejects manifest limits outside basis-aapl-v1", () => {
  const input = fixture();
  input.strategyManifest.max_leverage_ppm += 1;
  assert.throws(() => validateLivePolicy(input), /manifest limits differ/);
});

test("rejects a strategy policy consumer with a different commitment", () => {
  const input = fixture();
  const commitment = JSON.parse(input.strategyArtifacts.riskPolicy).minimumNetEdgePolicyCommitmentSha256;
  input.strategyArtifacts.policyConsumers.liveEvaluation =
    input.strategyArtifacts.policyConsumers.liveEvaluation.replace(commitment, "0".repeat(64));
  assert.throws(() => validateLivePolicy(input), /liveEvaluation strategy policy commitment/);
});
