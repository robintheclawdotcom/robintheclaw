#!/usr/bin/env node

import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const keys = [
  "approval_type",
  "executor_review_approved",
  "internal_audit_approved",
  "internal_audit_sha256",
  "key_review_approved",
  "max_accounts",
  "max_daily_turnover_micros",
  "max_gross_notional_micros",
  "max_leg_notional_micros",
  "max_leverage_ppm",
  "restore_drill_approved",
];

export function calculateEvidenceHash(evidence) {
  const u16 = Buffer.alloc(2);
  u16.writeUInt16BE(evidence.max_accounts);
  const u32 = Buffer.alloc(4);
  u32.writeUInt32BE(evidence.max_leverage_ppm);
  const u64 = (value) => {
    const bytes = Buffer.alloc(8);
    bytes.writeBigUInt64BE(BigInt(value));
    return bytes;
  };
  return createHash("sha256").update(Buffer.concat([
    Buffer.from("engineering-canary-v1"),
    u16,
    u64(evidence.max_leg_notional_micros),
    u64(evidence.max_gross_notional_micros),
    u64(evidence.max_daily_turnover_micros),
    u32,
    Buffer.from(evidence.internal_audit_sha256),
    Buffer.from([
      Number(evidence.internal_audit_approved),
      Number(evidence.executor_review_approved),
      Number(evidence.key_review_approved),
      Number(evidence.restore_drill_approved),
    ]),
  ])).digest("hex");
}

export function validateEngineeringCanary({ evidence, audit, migration, policy }) {
  const actualKeys = Object.keys(evidence).sort();
  if (actualKeys.length !== keys.length || actualKeys.some((key, index) => key !== [...keys].sort()[index])) {
    throw new Error("engineering canary evidence keys are not canonical");
  }
  if (evidence.approval_type !== "engineering_canary") {
    throw new Error("engineering canary approval type is invalid");
  }
  const bounds = {
    max_accounts: 1,
    max_leg_notional_micros: 25_000_000,
    max_gross_notional_micros: 50_000_000,
    max_daily_turnover_micros: 50_000_000,
    max_leverage_ppm: 1_000_000,
  };
  for (const [key, value] of Object.entries(bounds)) {
    if (evidence[key] !== value) throw new Error(`${key} differs from the approved canary scope`);
  }
  for (const key of [
    "internal_audit_approved",
    "executor_review_approved",
    "key_review_approved",
    "restore_drill_approved",
  ]) {
    if (evidence[key] !== true) throw new Error(`${key} is not approved`);
  }

  const auditHash = createHash("sha256").update(audit).digest("hex");
  if (evidence.internal_audit_sha256 !== auditHash) {
    throw new Error("engineering canary evidence does not bind the internal audit");
  }
  const evidenceHash = calculateEvidenceHash(evidence);
  if (count(migration, evidenceHash) !== 3 || count(migration, auditHash) !== 1) {
    throw new Error("canary migration is not bound to the canonical evidence and audit");
  }
  if (!migration.includes("'approval_type', 'engineering_canary'")
      || !migration.includes("'internal-release-audit-2026-07-14'")) {
    throw new Error("canary migration approval identity is invalid");
  }
  if (policy.rolloutStage !== "canary" || !policy.executionEnabled || !policy.capitalActivationAllowed) {
    throw new Error("mainnet canary policy is not enabled");
  }
  if (policy.limits.maxAccounts !== 1
      || policy.limits.maxLegNotionalMicros !== bounds.max_leg_notional_micros
      || policy.limits.maxGrossNotionalMicros !== bounds.max_gross_notional_micros
      || policy.limits.maxDailyTurnoverMicros !== bounds.max_daily_turnover_micros
      || policy.limits.maxLeveragePpm !== bounds.max_leverage_ppm) {
    throw new Error("mainnet policy differs from the approved canary evidence");
  }
  return { auditHash, evidenceHash };
}

function count(value, needle) {
  return value.split(needle).length - 1;
}

function main() {
  const root = join(dirname(fileURLToPath(import.meta.url)), "..");
  const result = validateEngineeringCanary({
    evidence: JSON.parse(readFileSync(join(root, "config", "engineering-canary-evidence.json"), "utf8")),
    audit: readFileSync(join(root, "docs", "production-audit-mainnet-live-execution.md")),
    migration: readFileSync(join(root, "coordinator", "migrations", "0016_enable_basis_aapl_canary.sql"), "utf8"),
    policy: JSON.parse(readFileSync(join(root, "config", "mainnet-live-policy.json"), "utf8")),
  });
  console.log(`engineering canary evidence is valid (${result.evidenceHash})`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) main();
