#!/usr/bin/env node

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { createPublicClient, getAddress, http } from "viem";
import { verifyAgainstChain } from "./onchain.mjs";

const verifierRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const root = join(verifierRoot, "..");
const deployment = JSON.parse(readFileSync(join(root, "deployments", "testnet-proof.json"), "utf8"));
const fixture = JSON.parse(readFileSync(join(verifierRoot, "fixtures", "testnet-proof-batch.json"), "utf8"));

if (deployment.chainId !== 46630) throw new Error("testnet proof deployment has the wrong chain ID");
if (fixture.kind !== "attestation-pipeline-proof") throw new Error("unexpected proof fixture");
const rpc = "https://rpc.testnet.chain.robinhood.com/rpc";
const client = createPublicClient({ transport: http(rpc) });
const vaultAbi = [
  { name: "asset", type: "function", stateMutability: "view", inputs: [], outputs: [{ type: "address" }] },
  { name: "guard", type: "function", stateMutability: "view", inputs: [], outputs: [{ type: "address" }] },
  { name: "owner", type: "function", stateMutability: "view", inputs: [], outputs: [{ type: "address" }] },
  { name: "agent", type: "function", stateMutability: "view", inputs: [], outputs: [{ type: "address" }] },
  { name: "attestationAnchor", type: "function", stateMutability: "view", inputs: [], outputs: [{ type: "address" }] },
];
const guardAbi = [
  { name: "owner", type: "function", stateMutability: "view", inputs: [], outputs: [{ type: "address" }] },
  { name: "executor", type: "function", stateMutability: "view", inputs: [], outputs: [{ type: "address" }] },
];
const anchorAbi = [
  { name: "publisher", type: "function", stateMutability: "view", inputs: [], outputs: [{ type: "address" }] },
];
const [asset, guard, owner, agent, anchor, guardOwner, executor, publisher] = await Promise.all([
  client.readContract({ address: getAddress(deployment.strategyVault), abi: vaultAbi, functionName: "asset" }),
  client.readContract({ address: getAddress(deployment.strategyVault), abi: vaultAbi, functionName: "guard" }),
  client.readContract({ address: getAddress(deployment.strategyVault), abi: vaultAbi, functionName: "owner" }),
  client.readContract({ address: getAddress(deployment.strategyVault), abi: vaultAbi, functionName: "agent" }),
  client.readContract({ address: getAddress(deployment.strategyVault), abi: vaultAbi, functionName: "attestationAnchor" }),
  client.readContract({ address: getAddress(deployment.mandateGuard), abi: guardAbi, functionName: "owner" }),
  client.readContract({ address: getAddress(deployment.mandateGuard), abi: guardAbi, functionName: "executor" }),
  client.readContract({ address: getAddress(deployment.attestationAnchor), abi: anchorAbi, functionName: "publisher" }),
]);

for (const [actual, expected, name] of [
  [asset, deployment.asset, "vault asset"],
  [guard, deployment.mandateGuard, "vault guard"],
  [owner, deployment.owner, "vault owner"],
  [agent, deployment.agent, "vault agent"],
  [anchor, deployment.attestationAnchor, "vault anchor"],
  [guardOwner, deployment.owner, "guard owner"],
  [executor, deployment.strategyVault, "guard executor"],
  [publisher, deployment.strategyVault, "anchor publisher"],
]) {
  assert.equal(getAddress(actual), getAddress(expected), `${name} mismatch`);
}

const result = await verifyAgainstChain({
  rpc,
  anchor: deployment.attestationAnchor,
  sequence: 1,
  records: fixture.records,
});

assert.equal(result.ok, true, result.reason);
assert.equal(result.onchainRoot, deployment.proofRoot, "deployment root mismatch");
console.log(`testnet proof verified: sequence ${result.onchain.sequence}, root ${result.onchainRoot}`);
