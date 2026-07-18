import assert from "node:assert/strict";
import test from "node:test";

import { policyCommitment } from "./generate-mainnet-strategy-policy.mjs";

test("strategy policy commitment is deterministic and domain separated", () => {
  const salt = "ab".repeat(32);
  const first = policyCommitment(1_200, salt);
  assert.match(first, /^[0-9a-f]{64}$/);
  assert.equal(first, policyCommitment(1_200, salt));
  assert.notEqual(first, policyCommitment(1_201, salt));
  assert.notEqual(first, policyCommitment(1_200, "cd".repeat(32)));
});

test("strategy policy commitment rejects invalid values", () => {
  assert.throws(() => policyCommitment(0, "ab".repeat(32)), /minimum net edge/);
  assert.throws(() => policyCommitment(1_200, "ab"), /salt/);
  assert.throws(() => policyCommitment(1_200, "AB".repeat(32)), /salt/);
});
