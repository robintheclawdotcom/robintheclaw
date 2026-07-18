import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { calculateEvidenceHash, validateEngineeringCanary } from "./validate-engineering-canary.mjs";

const evidence = JSON.parse(readFileSync("config/engineering-canary-evidence.json", "utf8"));
const audit = readFileSync("docs/production-audit-mainnet-live-execution.md");
const migration = readFileSync("coordinator/migrations/0017_refresh_basis_aapl_canary.sql", "utf8");
const policy = JSON.parse(readFileSync("config/mainnet-live-policy.json", "utf8"));

test("binds the enabled canary to the internal audit", () => {
  const result = validateEngineeringCanary({ evidence, audit, migration, policy });
  assert.equal(result.evidenceHash, "2a6bc1f8b43d24714e83a478a4c454439bd6cebedca334bd37963924d3ab9711");
  assert.equal(calculateEvidenceHash(evidence), result.evidenceHash);
});

test("rejects changed audit bytes", () => {
  assert.throws(
    () => validateEngineeringCanary({ evidence, audit: Buffer.concat([audit, Buffer.from("changed")]), migration, policy }),
    /does not bind/,
  );
});

test("rejects disabled execution", () => {
  assert.throws(
    () => validateEngineeringCanary({
      evidence,
      audit,
      migration,
      policy: { ...policy, executionEnabled: false },
    }),
    /not enabled/,
  );
});
