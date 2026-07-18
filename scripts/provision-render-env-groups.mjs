#!/usr/bin/env node

import { randomBytes } from "node:crypto";
import { readFile, stat } from "node:fs/promises";
import process from "node:process";

export const hexGroups = {
  "robin-lighter-provisioner-auth": ["LIGHTER_PROVISIONER_HMAC_KEY"],
  "robin-readiness-publisher-auth": ["READINESS_HMAC_KEY"],
  "robin-lighter-signer-auth": ["LIGHTER_SIGNER_HMAC_KEY"],
  "robin-lighter-signer-bridge-auth": ["LIGHTER_SIGNER_BRIDGE_HMAC_KEY"],
  "robin-lighter-publisher-bridge-auth": ["LIGHTER_PUBLISHER_BRIDGE_HMAC_KEY"],
  "robin-coordinator-episode-auth": ["COORDINATOR_EPISODE_HMAC_KEY"],
  "robin-robinhood-signer-auth": ["ROBINHOOD_SIGNER_HMAC_KEY"],
  "robin-robinhood-provisioner-auth": ["ROBINHOOD_PROVISIONER_HMAC_KEY"],
  "robin-robinhood-signer-bridge-auth": ["ROBINHOOD_SIGNER_BRIDGE_HMAC_KEY"],
  "robin-coordinator-intent-auth": ["COORDINATOR_INTENT_HMAC_KEY"],
  "robin-coordinator-exit-auth": ["COORDINATOR_EXIT_HMAC_KEY"],
  "robin-coordinator-venue-auth": ["COORDINATOR_VENUE_HMAC_KEY"],
  "robin-coordinator-market-auth": ["COORDINATOR_MARKET_HMAC_KEY"],
  "robin-coordinator-account-auth": ["COORDINATOR_ACCOUNT_HMAC_KEY"],
  "robin-coordinator-control-auth": ["COORDINATOR_CONTROL_HMAC_KEY"],
  "robin-coordinator-registration-auth": ["COORDINATOR_REGISTRATION_HMAC_KEY"],
};

export const configGroups = {
  "robin-lighter-market-config": ["LIGHTER_AAPL_MARKET_INDEX"],
  "robin-aapl-strategy-policy": [
    "AAPL_MINIMUM_NET_EDGE_PPM",
    "AAPL_STRATEGY_POLICY_SALT",
  ],
  "robin-quote-authority-robinhood-rpc": [
    "ROBINHOOD_RPC_URL",
    "ROBINHOOD_RECONCILIATION_RPC_URL",
  ],
  "robin-lighter-market-spec": [
    "LIGHTER_AAPL_BASE_DECIMALS",
    "LIGHTER_AAPL_PRICE_DECIMALS",
  ],
  "robin-aapl-reference-feed-config": [
    "AAPL_REFERENCE_FEED",
    "AAPL_REFERENCE_FEED_CODE_HASH",
    "AAPL_SOURCE_FEED_CODE_HASH",
    "AAPL_SOURCE_AGGREGATOR",
    "AAPL_SOURCE_AGGREGATOR_CODE_HASH",
    "AAPL_REFERENCE_FEED_DECIMALS",
    "AAPL_REFERENCE_FEED_HEARTBEAT_SECONDS",
  ],
  "robin-quote-authority-public-key": ["ROBIN_QUOTE_AUTHORITY_ED25519_PUBLIC_KEY"],
  "robin-sequencer-feed-config": [
    "SEQUENCER_FEED_ADDRESS",
    "SEQUENCER_FEED_CODE_HASH",
    "SEQUENCER_USDG_PROXY_CODE_HASH",
    "SEQUENCER_USDG_IMPLEMENTATION_ADDRESS",
    "SEQUENCER_USDG_IMPLEMENTATION_CODE_HASH",
    "SEQUENCER_AAPL_PROXY_CODE_HASH",
    "SEQUENCER_AAPL_BEACON_ADDRESS",
    "SEQUENCER_AAPL_BEACON_CODE_HASH",
    "SEQUENCER_AAPL_IMPLEMENTATION_ADDRESS",
    "SEQUENCER_AAPL_IMPLEMENTATION_CODE_HASH",
  ],
};

export function validateConfig(value) {
  const groups = value?.groups;
  if (!groups || typeof groups !== "object" || Array.isArray(groups)) {
    throw new Error("config must contain a groups object");
  }
  const actualGroups = Object.keys(groups).sort();
  const expectedGroups = Object.keys(configGroups).sort();
  if (JSON.stringify(actualGroups) !== JSON.stringify(expectedGroups)) {
    throw new Error("config group names do not match the reviewed manifest");
  }
  for (const [name, expectedKeys] of Object.entries(configGroups)) {
    const variables = groups[name];
    if (!variables || typeof variables !== "object" || Array.isArray(variables)) {
      throw new Error(`${name} must be an object`);
    }
    const actualKeys = Object.keys(variables).sort();
    if (JSON.stringify(actualKeys) !== JSON.stringify([...expectedKeys].sort())) {
      throw new Error(`${name} keys do not match the reviewed manifest`);
    }
    for (const key of expectedKeys) {
      if (typeof variables[key] !== "string" || variables[key].trim() === "") {
        throw new Error(`${name}.${key} must be non-empty`);
      }
    }
  }
  return groups;
}

export function indexGroups(values) {
  return new Map(values.map((value) => {
    const group = value.envGroup ?? value;
    if (!group?.id || !group?.name) {
      throw new Error("Render env-group response is invalid");
    }
    return [group.name, group];
  }));
}

export function validateGroupVariables(name, variables, expectedKeys, kind) {
  if (!Array.isArray(variables)) {
    throw new Error(`${name} variables are invalid`);
  }
  const actual = variables.map((variable) => variable.key).sort();
  const expected = [...expectedKeys].sort();
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    throw new Error(`${name} keys do not match the reviewed manifest`);
  }
  for (const variable of variables) {
    if (kind === "auth") {
      if (!/^[0-9a-f]{64}$/.test(variable.value ?? "")) {
        throw new Error(`${name}.${variable.key} must be 32-byte lowercase hex`);
      }
      continue;
    }
    if (typeof variable.value !== "string" || variable.value.trim() === "") {
      throw new Error(`${name}.${variable.key} must be non-empty`);
    }
  }
}

export function configChanges(name, variables, desired) {
  if (!Array.isArray(variables)) {
    throw new Error(`${name} variables are invalid`);
  }
  const current = new Map();
  for (const variable of variables) {
    if (typeof variable?.key !== "string" || current.has(variable.key)) {
      throw new Error(`${name} variables are invalid`);
    }
    current.set(variable.key, variable.value);
  }
  const upserts = Object.entries(desired)
    .filter(([key, value]) => current.get(key) !== value)
    .map(([key, value]) => ({ key, value }));
  const removals = [...current.keys()].filter((key) => !Object.hasOwn(desired, key));
  return { upserts, removals };
}

export function validateConfigValues(name, variables, desired) {
  validateGroupVariables(name, variables, Object.keys(desired), "config");
  const current = new Map(variables.map((variable) => [variable.key, variable.value]));
  for (const [key, value] of Object.entries(desired)) {
    if (current.get(key) !== value) {
      throw new Error(`${name}.${key} does not match the reviewed value`);
    }
  }
}

function parseArgs(argv) {
  const [mode, ...rest] = argv;
  if (!["auth", "config", "verify"].includes(mode)) {
    throw new Error("usage: provision-render-env-groups.mjs auth|config|verify [options]");
  }
  const options = { mode };
  for (let index = 0; index < rest.length; index += 2) {
    const flag = rest[index];
    const value = rest[index + 1];
    if (!value || !["--owner", "--token-file", "--config"].includes(flag)) {
      throw new Error(`invalid argument: ${flag ?? ""}`);
    }
    options[flag.slice(2).replace("-", "_")] = value;
  }
  if (!options.owner || !options.token_file) {
    throw new Error("--owner and --token-file are required");
  }
  if (mode === "config" && !options.config) {
    throw new Error("--config is required in config mode");
  }
  return options;
}

async function readToken(path) {
  const metadata = await stat(path);
  if ((metadata.mode & 0o077) !== 0) {
    throw new Error("Render token file must be mode 0600");
  }
  const token = (await readFile(path, "utf8")).trim();
  if (!token) {
    throw new Error("Render token file is empty");
  }
  return token;
}

async function request(token, path, init = {}) {
  const response = await fetch(`https://api.render.com/v1${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
      ...init.headers,
    },
  });
  if (!response.ok) {
    throw new Error(`Render API ${init.method ?? "GET"} ${path} failed with ${response.status}`);
  }
  if (response.status === 204) {
    return null;
  }
  return await response.json();
}

async function listGroups(token, owner) {
  const values = await request(token, `/env-groups?ownerId=${encodeURIComponent(owner)}&limit=100`);
  return indexGroups(values);
}

async function verifyGroup(token, metadata, expectedKeys, kind) {
  const group = await request(token, `/env-groups/${encodeURIComponent(metadata.id)}`);
  validateGroupVariables(metadata.name, group.envVars, expectedKeys, kind);
}

async function convergeConfigGroup(token, metadata, desired) {
  const id = encodeURIComponent(metadata.id);
  const group = await request(token, `/env-groups/${id}`);
  const changes = configChanges(metadata.name, group.envVars, desired);
  for (const variable of changes.upserts) {
    await request(token, `/env-groups/${id}/env-vars/${encodeURIComponent(variable.key)}`, {
      method: "PUT",
      body: JSON.stringify({ value: variable.value }),
    });
  }
  for (const key of changes.removals) {
    await request(token, `/env-groups/${id}/env-vars/${encodeURIComponent(key)}`, {
      method: "DELETE",
    });
  }
  const updated = await request(token, `/env-groups/${id}`);
  validateConfigValues(metadata.name, updated.envVars, desired);
  return changes.upserts.length + changes.removals.length;
}

async function createGroup(token, owner, name, values) {
  await request(token, "/env-groups", {
    method: "POST",
    body: JSON.stringify({
      name,
      ownerId: owner,
      envVars: Object.entries(values).map(([key, value]) => ({ key, value })),
    }),
  });
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  const token = await readToken(options.token_file);
  const existing = await listGroups(token, options.owner);
  let desired;
  if (options.mode === "auth") {
    desired = Object.fromEntries(
      Object.entries(hexGroups).map(([name, keys]) => [
        name,
        Object.fromEntries(keys.map((key) => [key, randomBytes(32).toString("hex")])),
      ]),
    );
  } else if (options.mode === "config") {
    desired = validateConfig(JSON.parse(await readFile(options.config, "utf8")));
  } else {
    desired = Object.fromEntries(
      [...Object.entries(hexGroups), ...Object.entries(configGroups)].map(([name, keys]) => [
        name,
        Object.fromEntries(keys.map((key) => [key, "redacted"])),
      ]),
    );
  }

  for (const [name, values] of Object.entries(desired)) {
    const group = existing.get(name);
    if (group) {
      if (options.mode === "config") {
        const count = await convergeConfigGroup(token, group, values);
        console.log(`${count === 0 ? "verified" : "updated"} ${name}`);
        continue;
      }
      const kind = Object.hasOwn(hexGroups, name) ? "auth" : "config";
      await verifyGroup(token, group, Object.keys(values), kind);
      console.log(`verified ${name}`);
      continue;
    }
    if (options.mode === "verify") {
      throw new Error(`${name} is missing`);
    }
    await createGroup(token, options.owner, name, values);
    console.log(`created ${name}`);
  }
}

const invoked = process.argv[1] && new URL(import.meta.url).pathname === process.argv[1];
if (invoked) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
