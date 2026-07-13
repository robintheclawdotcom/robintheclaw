import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { validateLivePolicy } from "./validate-live-policy.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

function fixture() {
  return {
    policy: JSON.parse(readFileSync(join(root, "config", "mainnet-live-policy.json"), "utf8")),
    paper: JSON.parse(readFileSync(join(root, "runtime", "config", "mainnet-paper.json"), "utf8")),
    render: readFileSync(join(root, "render.yaml"), "utf8"),
    personalVault: readFileSync(join(root, "contracts", "src", "PersonalStrategyVault.sol"), "utf8"),
  };
}

test("accepts the disabled build policy", () => {
  assert.doesNotThrow(() => validateLivePolicy(fixture()));
});

test("rejects activation with an open gate", () => {
  const input = fixture();
  input.policy.executionEnabled = true;
  input.policy.capitalActivationAllowed = true;
  input.policy.rolloutStage = "canary";
  input.policy.limits.maxAccounts = 1;
  input.policy.limits.maxAggregatePerVenueMicros = 25_000_000;
  assert.throws(() => validateLivePolicy(input), /open gate/);
});

test("rejects a raised account cap", () => {
  const input = fixture();
  input.policy.limits.maxLegNotionalMicros += 1;
  assert.throws(() => validateLivePolicy(input), /differs from the v1 strategy policy/);
});

test("rejects a blueprint with an enabled signer", () => {
  const input = fixture();
  input.render = input.render.replace(
    /key:\s*LIGHTER_SIGNER_ENABLED\s*\n\s*value:\s*["']?false["']?/,
    'key: LIGHTER_SIGNER_ENABLED\n        value: "true"',
  );
  assert.throws(() => validateLivePolicy(input), /LIGHTER_SIGNER_ENABLED must remain false/);
});
