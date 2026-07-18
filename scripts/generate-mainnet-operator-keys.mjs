#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { generateKeyPairSync } from "node:crypto";
import { open } from "node:fs/promises";
import process from "node:process";

function decodeBase64URL(value) {
  return Buffer.from(value, "base64url");
}

export function generateQuoteKey() {
  const { privateKey } = generateKeyPairSync("ed25519");
  const jwk = privateKey.export({ format: "jwk" });
  const seed = decodeBase64URL(jwk.d);
  const publicKey = decodeBase64URL(jwk.x);
  if (seed.length !== 32 || publicKey.length !== 32) {
    throw new Error("generated Ed25519 key has an unexpected length");
  }
  return {
    privateKeyBase64: Buffer.concat([seed, publicKey]).toString("base64"),
    publicKeyBase64: publicKey.toString("base64"),
  };
}

function generateEVMKeys(count) {
  const wallets = JSON.parse(
    execFileSync("cast", ["wallet", "new", "--json", "--number", String(count)], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    }),
  );
  if (
    !Array.isArray(wallets) ||
    wallets.length !== count ||
    wallets.some(
      (wallet) =>
        !/^0x[0-9a-fA-F]{40}$/.test(wallet.address) ||
        !/^0x[0-9a-fA-F]{64}$/.test(wallet.private_key),
    )
  ) {
    throw new Error("cast returned an invalid EVM key bundle");
  }
  return wallets.map((wallet) => ({
    address: wallet.address,
    privateKey: wallet.private_key,
  }));
}

export function validateBundle(bundle) {
  if (
    bundle?.version !== 1 ||
    Buffer.from(bundle.quoteAuthority?.privateKeyBase64 ?? "", "base64").length !== 64 ||
    Buffer.from(bundle.quoteAuthority?.publicKeyBase64 ?? "", "base64").length !== 32
  ) {
    throw new Error("operator key bundle has an invalid quote key");
  }
  for (const name of ["sequencerPublishers", "aaplPublishers"]) {
    const keys = bundle[name];
    if (
      !Array.isArray(keys) ||
      keys.length !== 3 ||
      keys.some(
        (key) =>
          !/^0x[0-9a-fA-F]{40}$/.test(key.address) ||
          !/^0x[0-9a-fA-F]{64}$/.test(key.privateKey),
      ) ||
      new Set(keys.map((key) => key.address.toLowerCase())).size !== 3
    ) {
      throw new Error(`operator key bundle has invalid ${name}`);
    }
  }
  return bundle;
}

async function main() {
  const index = process.argv.indexOf("--out");
  const output = index >= 0 ? process.argv[index + 1] : "";
  if (!output || process.argv.length !== 4) {
    throw new Error("usage: generate-mainnet-operator-keys.mjs --out <ignored-mode-0600-path>");
  }
  const bundle = validateBundle({
    version: 1,
    quoteAuthority: generateQuoteKey(),
    sequencerPublishers: generateEVMKeys(3),
    aaplPublishers: generateEVMKeys(3),
  });
  const file = await open(output, "wx", 0o600);
  try {
    await file.writeFile(`${JSON.stringify(bundle, null, 2)}\n`, { encoding: "utf8" });
  } finally {
    await file.close();
  }
  console.log("generated mainnet operator key bundle");
}

const invoked = process.argv[1] && new URL(import.meta.url).pathname === process.argv[1];
if (invoked) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
