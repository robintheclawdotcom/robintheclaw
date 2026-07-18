#!/usr/bin/env node

import { createHash, randomBytes } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import process from "node:process";

const strategyVersion = "basis-aapl-v1";

export function policyCommitment(minimumNetEdgePpm, saltHex) {
  if (!Number.isSafeInteger(minimumNetEdgePpm) || minimumNetEdgePpm < 1 || minimumNetEdgePpm > 1_000_000) {
    throw new Error("minimum net edge must be between 1 and 1000000 ppm");
  }
  if (!/^[0-9a-f]{64}$/.test(saltHex)) {
    throw new Error("strategy policy salt must be 32-byte lowercase hex");
  }
  const canonical = `${strategyVersion}\0minimum_net_edge_ppm\0${minimumNetEdgePpm}\0${saltHex}`;
  return createHash("sha256").update(canonical).digest("hex");
}

function parseArgs(argv) {
  const options = {};
  for (let index = 0; index < argv.length; index += 2) {
    const flag = argv[index];
    const value = argv[index + 1];
    if (!value || !["--out", "--minimum-net-edge-ppm"].includes(flag)) {
      throw new Error(`invalid argument: ${flag ?? ""}`);
    }
    options[flag.slice(2).replaceAll("-", "_")] = value;
  }
  const minimumNetEdgePpm = Number.parseInt(options.minimum_net_edge_ppm, 10);
  if (!options.out || !Number.isSafeInteger(minimumNetEdgePpm)) {
    throw new Error("usage: generate-mainnet-strategy-policy.mjs --out PATH --minimum-net-edge-ppm N");
  }
  return { out: resolve(options.out), minimumNetEdgePpm };
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  const saltHex = randomBytes(32).toString("hex");
  const policy = {
    schemaVersion: 1,
    strategyVersion,
    minimumNetEdgePpm: options.minimumNetEdgePpm,
    saltHex,
    commitmentSha256: policyCommitment(options.minimumNetEdgePpm, saltHex),
  };
  await mkdir(dirname(options.out), { recursive: true });
  await writeFile(options.out, `${JSON.stringify(policy, null, 2)}\n`, { flag: "wx", mode: 0o600 });
  console.log(`wrote ${options.out}`);
}

const invoked = process.argv[1] && resolve(process.argv[1]) === resolve(new URL(import.meta.url).pathname);
if (invoked) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
