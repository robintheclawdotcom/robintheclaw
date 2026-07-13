import assert from "node:assert/strict";
import { createHash, generateKeyPairSync } from "node:crypto";
import { chmodSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { fileURLToPath } from "node:url";
import {
  closeIncident,
  initializeLedger,
  promoteLedger,
  recordIncident,
  retireLedger,
  startObservation,
  verifyLedger,
} from "./ledger.mjs";

function fixture() {
  const directory = mkdtempSync(join(tmpdir(), "robin-promotion-ledger-"));
  const { privateKey, publicKey } = generateKeyPairSync("ed25519");
  const privateKeyPem = privateKey.export({ type: "pkcs8", format: "pem" });
  const publicKeyPem = publicKey.export({ type: "spki", format: "pem" });
  return {
    directory,
    ledgerPath: join(directory, "promotion.jsonl"),
    checkpointPath: join(directory, "promotion.checkpoint.json"),
    privateKey: privateKeyPem,
    publicKey: publicKeyPem,
    release: release(),
  };
}

function release() {
  return {
    strategyVersion: "basis-aapl-v1",
    strategyManifestSha256: digest("manifest"),
    routeSha256: digest("route"),
    oraclePolicySha256: digest("oracle"),
    riskPolicySha256: digest("risk"),
    codeCommit: "1".repeat(40),
    codeArtifactSha256: digest("code artifact"),
  };
}

function digest(value) {
  return createHash("sha256").update(value).digest("hex");
}

function at(second) {
  return new Date(`2026-01-01T00:00:${String(second).padStart(2, "0")}.000Z`);
}

function init(options) {
  return initializeLedger({
    ...options,
    evidenceSha256: digest("paper evidence"),
    now: at(0),
  });
}

test("rejects skipped promotion stages", () => {
  const options = fixture();
  init(options);
  assert.throws(
    () => promoteLedger({
      ...options,
      toStage: "canary",
      evidenceSha256: digest("invalid skip"),
      now: at(1),
    }),
    /invalid promotion transition paper -> canary/,
  );
  const verified = verifyLedger(options);
  assert.equal(verified.entries.length, 1);
  assert.equal(verified.state.stage, "paper");
});

test("rejects release digest changes", () => {
  const options = fixture();
  init(options);
  const changed = {
    ...options,
    release: { ...options.release, routeSha256: digest("substituted route") },
  };
  assert.throws(
    () => promoteLedger({
      ...changed,
      toStage: "shadow",
      evidenceSha256: digest("changed release"),
      now: at(1),
    }),
    /invalid promotion ledger identity/,
  );
  assert.equal(verifyLedger(options).entries.length, 1);
});

test("stage-failing incidents reset and explicitly restart observation", () => {
  const options = fixture();
  init(options);
  const promoted = promoteLedger({
    ...options,
    toStage: "shadow",
    evidenceSha256: digest("shadow promotion"),
    now: at(1),
  });
  assert.equal(promoted.state.cleanObservationStartedAt, at(1).toISOString());

  const incident = recordIncident({
    ...options,
    incidentId: "INC-0001",
    severity: "high",
    evidenceSha256: digest("high incident"),
    now: at(2),
  });
  assert.equal(incident.state.cleanObservationStartedAt, null);
  assert.throws(
    () => startObservation({
      ...options,
      evidenceSha256: digest("premature restart"),
      now: at(3),
    }),
    /stage-failing incident is open/,
  );

  const closed = closeIncident({
    ...options,
    incidentId: "INC-0001",
    evidenceSha256: digest("incident review"),
    now: at(4),
  });
  assert.equal(closed.state.cleanObservationStartedAt, null);
  const restarted = startObservation({
    ...options,
    evidenceSha256: digest("reconciliation and drills"),
    now: at(5),
  });
  assert.equal(restarted.state.cleanObservationStartedAt, at(5).toISOString());

  const warning = recordIncident({
    ...options,
    incidentId: "INC-0002",
    severity: "warning",
    evidenceSha256: digest("warning incident"),
    now: at(6),
  });
  assert.equal(warning.state.cleanObservationStartedAt, at(5).toISOString());
});

test("retirement is terminal from any active stage", () => {
  const options = fixture();
  init(options);
  const retired = retireLedger({
    ...options,
    evidenceSha256: digest("retirement approval"),
    now: at(1),
  });
  assert.equal(retired.state.stage, "retired");
  assert.equal(retired.state.cleanObservationStartedAt, null);
  assert.throws(
    () => promoteLedger({
      ...options,
      toStage: "shadow",
      evidenceSha256: digest("restart attempt"),
      now: at(2),
    }),
    /invalid promotion transition retired -> shadow/,
  );
  assert.throws(
    () => retireLedger({
      ...options,
      evidenceSha256: digest("second retirement"),
      now: at(2),
    }),
    /invalid strategy retirement/,
  );
});

test("verifies the complete signature chain before append", () => {
  const options = fixture();
  init(options);
  const entry = JSON.parse(readFileSync(options.ledgerPath, "utf8"));
  entry.event.evidenceSha256 = digest("tampered evidence");
  writeFileSync(options.ledgerPath, `${JSON.stringify(entry)}\n`, { mode: 0o600 });
  chmodSync(options.ledgerPath, 0o600);
  assert.throws(
    () => promoteLedger({
      ...options,
      toStage: "shadow",
      evidenceSha256: digest("promotion after tamper"),
      now: at(1),
    }),
    /invalid promotion ledger signature/,
  );
  assert.doesNotMatch(readFileSync(options.ledgerPath, "utf8"), /PRIVATE KEY/);
});

test("trusted checkpoint rejects valid-prefix rollback", () => {
  const options = fixture();
  init(options);
  promoteLedger({
    ...options,
    toStage: "shadow",
    evidenceSha256: digest("shadow promotion"),
    now: at(1),
  });
  const [first] = readFileSync(options.ledgerPath, "utf8").trimEnd().split("\n");
  writeFileSync(options.ledgerPath, `${first}\n`, { mode: 0o600 });
  chmodSync(options.ledgerPath, 0o600);
  assert.throws(
    () => verifyLedger(options),
    /behind or diverges from its trusted checkpoint/,
  );
});

test("operator CLI pins the canonical release and remains non-activating", () => {
  const options = fixture();
  const privateKeyPath = join(options.directory, "release-key.pem");
  const publicKeyPath = join(options.directory, "release-key.pub.pem");
  writeFileSync(privateKeyPath, options.privateKey, { mode: 0o600 });
  writeFileSync(publicKeyPath, options.publicKey, { mode: 0o644 });
  chmodSync(privateKeyPath, 0o600);
  const cli = fileURLToPath(new URL("./cli.mjs", import.meta.url));
  const codeSha256 = digest("canonical built artifact");
  const common = [
    "--ledger", options.ledgerPath,
    "--checkpoint", options.checkpointPath,
    "--public-key", publicKeyPath,
    "--code-sha256", codeSha256,
  ];
  const initialized = spawnSync(process.execPath, [
    cli,
    "init",
    ...common,
    "--private-key", privateKeyPath,
    "--evidence-sha256", digest("operator approval"),
  ], { encoding: "utf8" });
  assert.equal(initialized.status, 0, initialized.stderr);
  const status = JSON.parse(initialized.stdout);
  assert.equal(status.stage, "paper");
  assert.equal(status.executionEnabled, false);

  const verified = spawnSync(process.execPath, [cli, "verify", ...common], { encoding: "utf8" });
  assert.equal(verified.status, 0, verified.stderr);
  assert.equal(JSON.parse(verified.stdout).verifiedEntries, 1);
});
