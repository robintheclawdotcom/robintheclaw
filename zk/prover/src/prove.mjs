#!/usr/bin/env node
// Prover CLI: turn a trade batch into a zero-knowledge proof that the agent's net return over the
// batch cleared a threshold, revealing nothing about the individual trades. It orchestrates the
// Noir toolchain (nargo execute) and the proving backend (bb prove), holds no keys, and signs
// nothing. The proof is generated with the keccak oracle so it verifies both natively and through
// the on-chain Solidity verifier.
//
// Usage: node src/prove.mjs <batch.json> [--out <dir>]
//   batch.json: { "agentId": "0x..", "thresholdBps": 100, "blinding": "0x..",
//                 "trades": [{ "netPnlUsd": 3.0, "notionalUsd": 100.0 }, ...] }
// blinding is optional; a fresh random one is generated and recorded in claim.json when omitted.

import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, mkdirSync, copyFileSync, readFileSync, rmSync, cpSync } from "node:fs";
import { randomBytes } from "node:crypto";
import { tmpdir } from "node:os";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { encodeBatch, toProverToml, toField, FIELD_MODULUS } from "./encode.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const CIRCUIT_DIR = resolve(here, "..", "..", "circuits", "proof-of-pnl");
const CMD_TIMEOUT_MS = 300_000;
const MAX_BUFFER = 64 * 1024 * 1024;

function run(cmd, args, cwd) {
  try {
    return execFileSync(cmd, args, {
      cwd,
      stdio: ["ignore", "pipe", "pipe"],
      timeout: CMD_TIMEOUT_MS,
      maxBuffer: MAX_BUFFER,
    }).toString();
  } catch (err) {
    const detail = err.stderr?.toString().trim() || err.stdout?.toString().trim() || err.message;
    throw new Error(`${cmd} ${args.join(" ")} failed:\n${detail}`);
  }
}

function requireToolchain() {
  for (const tool of ["nargo", "bb"]) {
    try {
      execFileSync(tool, ["--version"], { stdio: "ignore" });
    } catch {
      throw new Error(
        `${tool} not found on PATH. Install the Noir toolchain (noirup) and Barretenberg (bbup); see zk/README.md.`,
      );
    }
  }
}

// A fresh non-zero field element for the commitment blinding, used when the caller supplies none.
function randomBlinding() {
  let v = 0n;
  while (v === 0n) v = BigInt(`0x${randomBytes(32).toString("hex")}`) % FIELD_MODULUS;
  return `0x${v.toString(16)}`;
}

export function prove(batch, outDir) {
  requireToolchain();
  const encoded = encodeBatch(batch);
  if (!encoded.meetsThreshold) {
    throw new Error(
      `net ${encoded.netReturnBps} bps is below the claimed ${encoded.thresholdBps} bps; nothing to prove`,
    );
  }
  const blinding = batch.blinding !== undefined ? toField(batch.blinding, "blinding") : randomBlinding();
  const toml = toProverToml({ encoded, blinding });

  const work = mkdtempSync(join(tmpdir(), "pnl-proof-"));
  try {
    // Run against an isolated copy of the circuit so the committed source tree is never mutated and
    // concurrent proofs cannot race on Prover.toml or target/.
    const circuit = join(work, "circuit");
    cpSync(CIRCUIT_DIR, circuit, {
      recursive: true,
      filter: (src) => !/[\\/]target([\\/]|$)/.test(src),
    });
    writeFileSync(join(circuit, "Prover.toml"), toml);
    run("nargo", ["execute", "witness"], circuit);

    const target = join(circuit, "target");
    const out = join(work, "out");
    run(
      "bb",
      [
        "prove",
        "--scheme",
        "ultra_honk",
        "--oracle_hash",
        "keccak",
        "--write_vk",
        "--verify",
        "-b",
        join(target, "proof_of_pnl.json"),
        "-w",
        join(target, "witness.gz"),
        "-o",
        out,
      ],
      circuit,
    );

    mkdirSync(outDir, { recursive: true });
    for (const f of ["proof", "public_inputs", "vk"]) copyFileSync(join(out, f), join(outDir, f));
    const proofHex = `0x${readFileSync(join(out, "proof")).toString("hex")}`;
    writeFileSync(join(outDir, "proof.hex"), proofHex);

    // The commitment is the circuit's public output: the final 32-byte field in public_inputs.
    const publicInputs = readFileSync(join(out, "public_inputs"));
    const commitment = `0x${publicInputs.subarray(publicInputs.length - 32).toString("hex")}`;

    const claim = {
      agentId: encoded.agentId,
      thresholdBps: encoded.thresholdBps.toString(),
      tradeCount: encoded.count,
      netReturnBps: encoded.netReturnBps.toString(),
      commitment,
      blinding,
    };
    writeFileSync(join(outDir, "claim.json"), `${JSON.stringify(claim, null, 2)}\n`);
    return claim;
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const args = process.argv.slice(2);
  const batchPath = args.find((a) => !a.startsWith("--"));
  const outIdx = args.indexOf("--out");
  const outDir = outIdx >= 0 ? args[outIdx + 1] : "proof-output";
  if (!batchPath) {
    console.error("usage: node src/prove.mjs <batch.json> [--out <dir>]");
    process.exit(2);
  }
  try {
    const batch = JSON.parse(readFileSync(batchPath, "utf8"));
    const claim = prove(batch, outDir);
    console.log(`proof written to ${outDir}`);
    console.log(
      `agent ${claim.agentId} · ${claim.tradeCount} trades · net ${claim.netReturnBps} bps · ` +
        `claim >= ${claim.thresholdBps} bps`,
    );
    console.log(`commitment ${claim.commitment}`);
  } catch (err) {
    console.error(`error: ${err.message}`);
    process.exit(1);
  }
}
