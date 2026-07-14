import {
  constants,
  closeSync,
  existsSync,
  fsyncSync,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  renameSync,
  rmdirSync,
  unlinkSync,
  writeFileSync,
} from "node:fs";
import {
  createHash,
  createPrivateKey,
  createPublicKey,
  randomBytes,
  sign,
  verify,
} from "node:crypto";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { calculateStrategyManifestHash } from "../../../config/validate-live-policy.mjs";

const DOMAIN = "robin.promotion-ledger.v1\0";
const STRATEGY_VERSION = "basis-aapl-v1";
const STAGES = ["paper", "shadow", "canary", "cohort", "public", "retired"];
const NEXT_STAGE = new Map([
  ["paper", new Set(["shadow", "canary"])],
  ["shadow", new Set(["canary"])],
  ["canary", new Set(["cohort"])],
  ["cohort", new Set(["public"])],
]);
const MODULE_DIR = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = join(MODULE_DIR, "..", "..", "..");
const MANIFEST_PATH = join(
  REPO_ROOT,
  "config",
  "strategies",
  "basis-aapl-v1.manifest.json",
);

export function loadCanonicalRelease(codeArtifactSha256) {
  assertSha256(codeArtifactSha256, "code artifact digest");
  const manifest = JSON.parse(readFileSync(MANIFEST_PATH, "utf8"));
  if (
    manifest.strategy_version !== STRATEGY_VERSION
    || manifest.sha256 !== calculateStrategyManifestHash(manifest)
    || !/^[0-9a-f]{40}$/.test(manifest.code_commit)
  ) {
    throw new Error("canonical strategy manifest is invalid");
  }
  const artifacts = [
    ["route_sha256", "basis-aapl-v1.route.json"],
    ["oracle_policy_sha256", "basis-aapl-v1.oracle-policy.json"],
    ["risk_policy_sha256", "basis-aapl-v1.risk-policy.json"],
  ];
  for (const [field, filename] of artifacts) {
    assertSha256(manifest[field], `manifest ${field}`);
    const path = join(REPO_ROOT, "config", "strategies", filename);
    if (sha256(readFileSync(path)) !== manifest[field]) {
      throw new Error(`canonical strategy ${field} does not match ${filename}`);
    }
  }
  return Object.freeze({
    strategyVersion: STRATEGY_VERSION,
    strategyManifestSha256: manifest.sha256,
    routeSha256: manifest.route_sha256,
    oraclePolicySha256: manifest.oracle_policy_sha256,
    riskPolicySha256: manifest.risk_policy_sha256,
    codeCommit: manifest.code_commit,
    codeArtifactSha256,
  });
}

export function initializeLedger(options) {
  const recordedAt = timestamp(options.now);
  return append(options, {
    type: "initialized",
    stage: "paper",
    evidenceSha256: evidence(options.evidenceSha256),
    cleanObservationStartedAt: recordedAt,
  }, { requireEmpty: true, recordedAt });
}

export function promoteLedger(options) {
  if (
    !STAGES.includes(options.toStage)
    || options.toStage === "paper"
    || options.toStage === "retired"
  ) {
    throw new Error("invalid promotion target");
  }
  const recordedAt = timestamp(options.now);
  return appendWithState(options, (state) => ({
    type: "promoted",
    fromStage: state.stage,
    toStage: options.toStage,
    evidenceSha256: evidence(options.evidenceSha256),
    cleanObservationStartedAt: recordedAt,
  }), recordedAt);
}

export function retireLedger(options) {
  return appendWithState(options, (state) => ({
    type: "retired",
    fromStage: state.stage,
    toStage: "retired",
    evidenceSha256: evidence(options.evidenceSha256),
    cleanObservationStartedAt: null,
  }), timestamp(options.now));
}

export function recordIncident(options) {
  const severity = options.severity;
  if (!new Set(["warning", "high", "critical"]).has(severity)) {
    throw new Error("invalid incident severity");
  }
  const stageFailing = severity === "high" || severity === "critical";
  return appendWithState(options, (state) => ({
    type: "incident_opened",
    incidentId: incidentId(options.incidentId),
    severity,
    stage: state.stage,
    stageFailing,
    evidenceSha256: evidence(options.evidenceSha256),
    cleanObservationStartedAt: stageFailing ? null : state.cleanObservationStartedAt,
  }), timestamp(options.now));
}

export function closeIncident(options) {
  return appendWithState(options, (state) => ({
    type: "incident_closed",
    incidentId: incidentId(options.incidentId),
    stage: state.stage,
    evidenceSha256: evidence(options.evidenceSha256),
    cleanObservationStartedAt: state.cleanObservationStartedAt,
  }), timestamp(options.now));
}

export function startObservation(options) {
  const recordedAt = timestamp(options.now);
  return appendWithState(options, (state) => ({
    type: "observation_started",
    stage: state.stage,
    evidenceSha256: evidence(options.evidenceSha256),
    cleanObservationStartedAt: recordedAt,
  }), recordedAt);
}

export function verifyLedger(options) {
  const publicKey = trustedPublicKey(options.publicKey);
  const release = validateRelease(options.release);
  const entries = readEntries(options.ledgerPath);
  if (entries.length === 0) throw new Error("promotion ledger is not initialized");
  if (options.checkpointPath && !existsSync(options.checkpointPath)) {
    throw new Error("trusted promotion checkpoint is missing");
  }
  const state = verifyEntries(entries, publicKey, release);
  verifyCheckpoint(options.checkpointPath, entries);
  return { entries, state };
}

function appendWithState(options, eventFactory, recordedAt) {
  return append(options, eventFactory, { requireEmpty: false, recordedAt });
}

function append(options, eventOrFactory, { requireEmpty, recordedAt }) {
  const ledgerPath = requiredPath(options.ledgerPath, "ledger path");
  const checkpointPath = requiredPath(options.checkpointPath, "checkpoint path");
  if (resolve(ledgerPath) === resolve(checkpointPath)) {
    throw new Error("ledger and checkpoint paths must be distinct");
  }
  const release = validateRelease(options.release);
  const privateKey = signingPrivateKey(options.privateKey);
  const publicKey = trustedPublicKey(options.publicKey);
  const derivedPublicKey = createPublicKey(privateKey);
  if (!publicKeysEqual(publicKey, derivedPublicKey)) {
    throw new Error("signing key does not match the trusted public key");
  }
  const lockPath = `${ledgerPath}.lock`;
  mkdirSync(dirname(ledgerPath), { recursive: true, mode: 0o700 });
  acquireLock(lockPath);
  try {
    const entries = readEntries(ledgerPath);
    if (requireEmpty && entries.length !== 0) {
      throw new Error("promotion ledger is already initialized");
    }
    if (!requireEmpty && entries.length === 0) {
      throw new Error("promotion ledger is not initialized");
    }
    const state = verifyEntries(entries, publicKey, release);
    if (entries.length !== 0 && !existsSync(checkpointPath)) {
      throw new Error("trusted promotion checkpoint is missing");
    }
    verifyCheckpoint(checkpointPath, entries);
    if (
      entries.length !== 0
      && parseTimestamp(recordedAt) < parseTimestamp(entries.at(-1).recordedAt)
    ) {
      throw new Error("promotion ledger time cannot move backwards");
    }
    const event = typeof eventOrFactory === "function" ? eventOrFactory(state) : eventOrFactory;
    const sequence = entries.length + 1;
    const previousEntryHash = entries.at(-1)?.entryHash ?? null;
    const signerKeyId = publicKeyId(publicKey);
    const unsigned = {
      schemaVersion: 1,
      sequence,
      recordedAt,
      release,
      event,
      previousEntryHash,
      signerKeyId,
    };
    const entryHash = sha256(`${DOMAIN}${canonicalJson(unsigned)}`);
    const signature = sign(null, Buffer.from(entryHash, "hex"), privateKey).toString("base64");
    const entry = { ...unsigned, entryHash, signature };
    const nextState = applyEntry(state, entry);
    appendAndSync(ledgerPath, `${canonicalJson(entry)}\n`);
    writeCheckpoint(checkpointPath, entry);
    return { entry, state: nextState };
  } finally {
    rmdirSync(lockPath);
  }
}

function verifyEntries(entries, publicKey, release) {
  let state = emptyState();
  let previousEntryHash = null;
  let previousTime = null;
  const signerKeyId = publicKeyId(publicKey);
  for (let index = 0; index < entries.length; index += 1) {
    const entry = entries[index];
    exactKeys(entry, [
      "entryHash",
      "event",
      "previousEntryHash",
      "recordedAt",
      "release",
      "schemaVersion",
      "sequence",
      "signature",
      "signerKeyId",
    ], "ledger entry");
    if (
      entry.schemaVersion !== 1
      || entry.sequence !== index + 1
      || entry.previousEntryHash !== previousEntryHash
      || entry.signerKeyId !== signerKeyId
      || canonicalJson(entry.release) !== canonicalJson(release)
    ) {
      throw new Error(`invalid promotion ledger identity at sequence ${index + 1}`);
    }
    const recordedAt = parseTimestamp(entry.recordedAt);
    if (previousTime !== null && recordedAt < previousTime) {
      throw new Error(`promotion ledger time moved backwards at sequence ${index + 1}`);
    }
    const unsigned = {
      schemaVersion: entry.schemaVersion,
      sequence: entry.sequence,
      recordedAt: entry.recordedAt,
      release: entry.release,
      event: entry.event,
      previousEntryHash: entry.previousEntryHash,
      signerKeyId: entry.signerKeyId,
    };
    const expectedHash = sha256(`${DOMAIN}${canonicalJson(unsigned)}`);
    if (
      entry.entryHash !== expectedHash
      || !isBase64(entry.signature)
      || !verify(null, Buffer.from(entry.entryHash, "hex"), publicKey, Buffer.from(entry.signature, "base64"))
    ) {
      throw new Error(`invalid promotion ledger signature at sequence ${index + 1}`);
    }
    state = applyEntry(state, entry);
    previousEntryHash = entry.entryHash;
    previousTime = recordedAt;
  }
  return state;
}

function applyEntry(current, entry) {
  const event = entry.event;
  if (!event || typeof event !== "object" || Array.isArray(event)) {
    throw new Error(`invalid promotion event at sequence ${entry.sequence}`);
  }
  const state = cloneState(current);
  switch (event.type) {
    case "initialized": {
      exactKeys(event, ["cleanObservationStartedAt", "evidenceSha256", "stage", "type"], event.type);
      if (entry.sequence !== 1 || current.stage !== null || event.stage !== "paper") {
        throw new Error("promotion ledger initialization is invalid");
      }
      validateEventEvidence(event);
      sameTimestamp(event.cleanObservationStartedAt, entry.recordedAt);
      state.stage = "paper";
      state.cleanObservationStartedAt = event.cleanObservationStartedAt;
      break;
    }
    case "promoted": {
      exactKeys(event, [
        "cleanObservationStartedAt",
        "evidenceSha256",
        "fromStage",
        "toStage",
        "type",
      ], event.type);
      validateEventEvidence(event);
      if (
        current.stage === null
        || current.stage === "retired"
        || event.fromStage !== current.stage
        || !NEXT_STAGE.get(current.stage)?.has(event.toStage)
        || current.cleanObservationStartedAt === null
        || current.openIncidents.size !== 0
      ) {
        throw new Error(`invalid promotion transition ${current.stage ?? "unset"} -> ${event.toStage}`);
      }
      sameTimestamp(event.cleanObservationStartedAt, entry.recordedAt);
      state.stage = event.toStage;
      state.cleanObservationStartedAt = event.cleanObservationStartedAt;
      break;
    }
    case "retired": {
      exactKeys(event, [
        "cleanObservationStartedAt",
        "evidenceSha256",
        "fromStage",
        "toStage",
        "type",
      ], event.type);
      validateEventEvidence(event);
      if (
        current.stage === null
        || current.stage === "retired"
        || event.fromStage !== current.stage
        || event.toStage !== "retired"
        || event.cleanObservationStartedAt !== null
      ) {
        throw new Error("invalid strategy retirement");
      }
      state.stage = "retired";
      state.cleanObservationStartedAt = null;
      break;
    }
    case "incident_opened": {
      exactKeys(event, [
        "cleanObservationStartedAt",
        "evidenceSha256",
        "incidentId",
        "severity",
        "stage",
        "stageFailing",
        "type",
      ], event.type);
      validateEventEvidence(event);
      incidentId(event.incidentId);
      if (
        current.stage === null
        || current.stage === "retired"
        || event.stage !== current.stage
        || current.incidentIds.has(event.incidentId)
      ) {
        throw new Error("invalid incident opening record");
      }
      const stageFailing = event.severity === "high" || event.severity === "critical";
      if (
        !new Set(["warning", "high", "critical"]).has(event.severity)
        || event.stageFailing !== stageFailing
      ) {
        throw new Error("incident severity and stage-failing status disagree");
      }
      const expectedStart = stageFailing ? null : current.cleanObservationStartedAt;
      if (event.cleanObservationStartedAt !== expectedStart) {
        throw new Error("incident clean-observation state is invalid");
      }
      state.openIncidents.set(event.incidentId, {
        severity: event.severity,
        stage: event.stage,
        stageFailing,
      });
      state.incidentIds.add(event.incidentId);
      state.cleanObservationStartedAt = expectedStart;
      break;
    }
    case "incident_closed": {
      exactKeys(event, [
        "cleanObservationStartedAt",
        "evidenceSha256",
        "incidentId",
        "stage",
        "type",
      ], event.type);
      validateEventEvidence(event);
      const incident = current.openIncidents.get(event.incidentId);
      if (
        !incident
        || event.stage !== current.stage
        || incident.stage !== current.stage
        || event.cleanObservationStartedAt !== current.cleanObservationStartedAt
      ) {
        throw new Error("invalid incident closure record");
      }
      state.openIncidents.delete(event.incidentId);
      break;
    }
    case "observation_started": {
      exactKeys(event, ["cleanObservationStartedAt", "evidenceSha256", "stage", "type"], event.type);
      validateEventEvidence(event);
      if (
        current.stage === null
        || current.stage === "retired"
        || event.stage !== current.stage
        || hasStageFailingIncident(current)
      ) {
        throw new Error("clean observation cannot start while a stage-failing incident is open");
      }
      sameTimestamp(event.cleanObservationStartedAt, entry.recordedAt);
      state.cleanObservationStartedAt = event.cleanObservationStartedAt;
      break;
    }
    default:
      throw new Error(`unknown promotion event type: ${event.type}`);
  }
  state.sequence = entry.sequence;
  state.entryHash = entry.entryHash;
  return state;
}

function readEntries(path) {
  if (!existsSync(path)) return [];
  assertPrivateFile(path, "promotion ledger");
  const raw = readFileSync(path, "utf8");
  if (raw === "") return [];
  if (!raw.endsWith("\n")) throw new Error("promotion ledger has a partial final record");
  return raw
    .slice(0, -1)
    .split("\n")
    .map((line, index) => {
      try {
        return JSON.parse(line);
      } catch {
        throw new Error(`promotion ledger record ${index + 1} is not valid JSON`);
      }
    });
}

function appendAndSync(path, line) {
  const existed = existsSync(path);
  const fd = openSync(path, constants.O_WRONLY | constants.O_CREAT | constants.O_APPEND, 0o600);
  try {
    writeFileSync(fd, line, "utf8");
    fsyncSync(fd);
  } finally {
    closeSync(fd);
  }
  assertPrivateFile(path, "promotion ledger");
  if (!existed) syncDirectory(dirname(path));
}

function writeCheckpoint(path, entry) {
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  const checkpoint = {
    schemaVersion: 1,
    strategyVersion: STRATEGY_VERSION,
    sequence: entry.sequence,
    entryHash: entry.entryHash,
  };
  const temporary = `${path}.tmp-${process.pid}-${randomBytes(8).toString("hex")}`;
  const fd = openSync(temporary, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL, 0o600);
  try {
    writeFileSync(fd, `${canonicalJson(checkpoint)}\n`, "utf8");
    fsyncSync(fd);
  } finally {
    closeSync(fd);
  }
  try {
    renameSync(temporary, path);
    syncDirectory(dirname(path));
    assertPrivateFile(path, "promotion checkpoint");
  } catch (error) {
    if (existsSync(temporary)) unlinkSync(temporary);
    throw error;
  }
}

function verifyCheckpoint(path, entries) {
  if (!path || !existsSync(path)) return;
  assertPrivateFile(path, "promotion checkpoint");
  let checkpoint;
  try {
    checkpoint = JSON.parse(readFileSync(path, "utf8"));
  } catch {
    throw new Error("promotion checkpoint is not valid JSON");
  }
  exactKeys(checkpoint, ["entryHash", "schemaVersion", "sequence", "strategyVersion"], "promotion checkpoint");
  if (
    checkpoint.schemaVersion !== 1
    || checkpoint.strategyVersion !== STRATEGY_VERSION
    || !Number.isSafeInteger(checkpoint.sequence)
    || checkpoint.sequence < 1
    || entries.length < checkpoint.sequence
    || entries[checkpoint.sequence - 1]?.entryHash !== checkpoint.entryHash
  ) {
    throw new Error("promotion ledger is behind or diverges from its trusted checkpoint");
  }
}

function validateRelease(release) {
  exactKeys(release, [
    "codeArtifactSha256",
    "codeCommit",
    "oraclePolicySha256",
    "riskPolicySha256",
    "routeSha256",
    "strategyManifestSha256",
    "strategyVersion",
  ], "release binding");
  if (release.strategyVersion !== STRATEGY_VERSION || !/^[0-9a-f]{40}$/.test(release.codeCommit)) {
    throw new Error("invalid promotion release identity");
  }
  for (const [label, value] of Object.entries(release)) {
    if (label.endsWith("Sha256")) assertSha256(value, label);
  }
  return Object.freeze({ ...release });
}

function trustedPublicKey(value) {
  try {
    const key = createPublicKey(value);
    if (key.asymmetricKeyType !== "ed25519") throw new Error();
    return key;
  } catch {
    throw new Error("invalid trusted promotion public key");
  }
}

function signingPrivateKey(value) {
  try {
    const key = createPrivateKey(value);
    if (key.asymmetricKeyType !== "ed25519") throw new Error();
    return key;
  } catch {
    throw new Error("invalid Ed25519 promotion signing key");
  }
}

function publicKeysEqual(left, right) {
  return left
    .export({ type: "spki", format: "der" })
    .equals(right.export({ type: "spki", format: "der" }));
}

function publicKeyId(key) {
  return sha256(key.export({ type: "spki", format: "der" }));
}

function acquireLock(path) {
  try {
    mkdirSync(path, { mode: 0o700 });
  } catch (error) {
    if (error?.code === "EEXIST") {
      throw new Error("promotion ledger is locked by another operator process");
    }
    throw error;
  }
}

function syncDirectory(path) {
  const fd = openSync(path, constants.O_RDONLY);
  try {
    fsyncSync(fd);
  } finally {
    closeSync(fd);
  }
}

function assertPrivateFile(path, label) {
  const stats = lstatSync(path);
  if (!stats.isFile() || stats.isSymbolicLink()) {
    throw new Error(`${label} must be a regular file`);
  }
  if ((stats.mode & 0o077) !== 0) {
    throw new Error(`${label} must not be group- or world-accessible`);
  }
}

function hasStageFailingIncident(state) {
  return [...state.openIncidents.values()].some((incident) => incident.stageFailing);
}

function emptyState() {
  return {
    sequence: 0,
    entryHash: null,
    stage: null,
    cleanObservationStartedAt: null,
    openIncidents: new Map(),
    incidentIds: new Set(),
  };
}

function cloneState(state) {
  return {
    ...state,
    openIncidents: new Map(state.openIncidents),
    incidentIds: new Set(state.incidentIds),
  };
}

function validateEventEvidence(event) {
  evidence(event.evidenceSha256);
}

function evidence(value) {
  assertSha256(value, "promotion evidence digest");
  return value;
}

function incidentId(value) {
  if (typeof value !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value)) {
    throw new Error("invalid incident identifier");
  }
  return value;
}

function timestamp(value = new Date()) {
  const date = value instanceof Date ? value : new Date(value);
  if (!Number.isFinite(date.getTime())) throw new Error("invalid event timestamp");
  return date.toISOString();
}

function parseTimestamp(value) {
  if (typeof value !== "string" || timestamp(value) !== value) {
    throw new Error("promotion entry timestamp is not canonical UTC");
  }
  return Date.parse(value);
}

function sameTimestamp(actual, expected) {
  if (actual !== expected) throw new Error("event timestamp does not match its ledger entry");
}

function assertSha256(value, label) {
  if (typeof value !== "string" || !/^[0-9a-f]{64}$/.test(value) || /^0{64}$/.test(value)) {
    throw new Error(`${label} must be a lowercase SHA-256 digest`);
  }
}

function requiredPath(value, label) {
  if (typeof value !== "string" || value.trim() === "") throw new Error(`${label} is required`);
  return value;
}

function exactKeys(value, expected, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${label} must be an object`);
  }
  const actual = Object.keys(value).sort();
  const required = [...expected].sort();
  if (actual.length !== required.length || actual.some((key, index) => key !== required[index])) {
    throw new Error(`${label} keys do not match the canonical schema`);
  }
}

function canonicalJson(value) {
  if (value === null || typeof value === "string" || typeof value === "boolean") {
    return JSON.stringify(value);
  }
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value)) throw new Error("canonical JSON numbers must be safe integers");
    return String(value);
  }
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(",")}]`;
  if (typeof value === "object") {
    const fields = Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${canonicalJson(value[key])}`);
    return `{${fields.join(",")}}`;
  }
  throw new Error("unsupported canonical JSON value");
}

function isBase64(value) {
  if (typeof value !== "string" || value === "") return false;
  try {
    return Buffer.from(value, "base64").toString("base64") === value;
  } catch {
    return false;
  }
}

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}
