#!/usr/bin/env node

import { existsSync, readFileSync, statSync } from "node:fs";
import {
  closeIncident,
  initializeLedger,
  loadCanonicalRelease,
  promoteLedger,
  recordIncident,
  retireLedger,
  startObservation,
  verifyLedger,
} from "./ledger.mjs";

const POLICY_PATH = new URL("../../../config/mainnet-live-policy.json", import.meta.url);

const [command, ...rawArguments] = process.argv.slice(2);

try {
  const arguments_ = parseArguments(rawArguments);
  rejectUnknownArguments(command, arguments_);
  const ledgerPath = required(arguments_, "ledger");
  const checkpointPath = required(arguments_, "checkpoint");
  const publicKeyPath = required(arguments_, "public-key");
  const codeArtifactSha256 = required(arguments_, "code-sha256");
  const release = loadCanonicalRelease(codeArtifactSha256);
  const publicKey = readFileSync(publicKeyPath, "utf8");

  if (command === "verify" || command === "status") {
    const result = verifyLedger({
      ledgerPath,
      checkpointPath,
      publicKey,
      release,
    });
    printStatus(result);
    process.exit(0);
  }

  const privateKeyPath = required(arguments_, "private-key");
  assertPrivateKeyFile(privateKeyPath);
  const options = {
    ledgerPath,
    checkpointPath,
    publicKey,
    privateKey: readFileSync(privateKeyPath, "utf8"),
    release,
    evidenceSha256: required(arguments_, "evidence-sha256"),
  };
  let result;
  switch (command) {
    case "init":
      result = initializeLedger(options);
      break;
    case "promote":
      result = promoteLedger({ ...options, toStage: required(arguments_, "to") });
      break;
    case "incident":
      result = recordIncident({
        ...options,
        incidentId: required(arguments_, "incident-id"),
        severity: required(arguments_, "severity"),
      });
      break;
    case "close-incident":
      result = closeIncident({ ...options, incidentId: required(arguments_, "incident-id") });
      break;
    case "start-observation":
      result = startObservation(options);
      break;
    case "retire":
      result = retireLedger(options);
      break;
    default:
      throw new Error(
        "command must be init, promote, incident, close-incident, start-observation, retire, verify, or status",
      );
  }
  printStatus({ entries: [result.entry], state: result.state });
} catch (error) {
  console.error(`promotion ledger: ${error.message}`);
  process.exit(1);
}

function parseArguments(values) {
  const parsed = new Map();
  for (let index = 0; index < values.length; index += 2) {
    const name = values[index];
    const value = values[index + 1];
    if (!name?.startsWith("--") || value === undefined || value.startsWith("--")) {
      throw new Error("arguments must be --name value pairs");
    }
    const key = name.slice(2);
    if (parsed.has(key)) throw new Error(`duplicate --${key}`);
    parsed.set(key, value);
  }
  return parsed;
}

function required(arguments_, key) {
  const value = arguments_.get(key);
  if (!value) throw new Error(`--${key} is required`);
  return value;
}

function rejectUnknownArguments(command, arguments_) {
  const readOnly = ["ledger", "checkpoint", "public-key", "code-sha256"];
  const mutation = [...readOnly, "private-key", "evidence-sha256"];
  let allowed = mutation;
  if (command === "promote") allowed = [...mutation, "to"];
  if (command === "incident") allowed = [...mutation, "incident-id", "severity"];
  if (command === "close-incident") allowed = [...mutation, "incident-id"];
  if (command === "verify" || command === "status") allowed = readOnly;
  const allowedSet = new Set(allowed);
  for (const key of arguments_.keys()) {
    if (!allowedSet.has(key)) {
      throw new Error(`--${key} is not valid for ${command ?? "this command"}`);
    }
  }
}

function assertPrivateKeyFile(path) {
  if (!existsSync(path) || (statSync(path).mode & 0o077) !== 0) {
    throw new Error("promotion signing key must be an existing mode-0600 file");
  }
}

function printStatus({ state }) {
  const policy = JSON.parse(readFileSync(POLICY_PATH, "utf8"));
  const executionEnabled = policy.executionEnabled === true
    && policy.capitalActivationAllowed === true
    && state.stage === policy.rolloutStage
    && state.openIncidents.size === 0;
  console.log(JSON.stringify({
    strategyVersion: "basis-aapl-v1",
    sequence: state.sequence,
    entryHash: state.entryHash,
    stage: state.stage,
    cleanObservationStartedAt: state.cleanObservationStartedAt,
    openIncidentCount: state.openIncidents.size,
    verifiedEntries: state.sequence,
    executionEnabled,
  }));
}
