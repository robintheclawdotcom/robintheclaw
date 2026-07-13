#!/usr/bin/env node
// Prover CLI: turn a trade batch into a zero-knowledge proof that the agent's net return over the
// batch cleared a threshold, revealing nothing about the individual trades. Orchestrates the Noir
// toolchain (nargo execute) and the proving backend (bb prove); it holds no keys and signs
// nothing. Output is a proof, its public inputs, and the committed batch's public parameters.
//
// Usage: node src/prove.mjs <batch.json> [--out <dir>]
//   batch.json: { "agentId": "0x..", "thresholdBps": 100, "blinding": "0x..",
//                 "trades": [{ "netPnlUsd": 3.0, "notionalUsd": 100.0 }, ...] }

import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, mkdirSync, copyFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { encodeBatch, toProverToml } from "./encode.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const CIRCUIT_DIR = resolve(here, "..", "..", "circuits", "proof-of-pnl");

function run(cmd, args, cwd) {
  return execFileSync(cmd, args, { cwd, stdio: ["ignore", "pipe", "pipe"] }).toString();
}

export function prove(batch, outDir) {
  if (batch.blinding === undefined) throw new Error("batch requires a per-proof blinding field");
  const encoded = encodeBatch(batch);
  const toml = toProverToml({ encoded, blinding: batch.blinding });

  const work = mkdtempSync(join(tmpdir(), "pnl-proof-"));
  try {
    writeFileSync(join(CIRCUIT_DIR, "Prover.toml"), toml);
    run("nargo", ["execute", "witness"], CIRCUIT_DIR);
    const target = join(CIRCUIT_DIR, "target");
    run(
      "bb",
      [
        "prove",
        "--scheme",
        "ultra_honk",
        "--write_vk",
        "--verify",
        "-b",
        join(target, "proof_of_pnl.json"),
        "-w",
        join(target, "witness.gz"),
        "-o",
        work,
      ],
      CIRCUIT_DIR,
    );
    mkdirSync(outDir, { recursive: true });
    for (const f of ["proof", "public_inputs", "vk"]) copyFileSync(join(work, f), join(outDir, f));
    const claim = {
      agentId: encoded.agentId,
      thresholdBps: encoded.thresholdBps.toString(),
      tradeCount: encoded.count,
      netReturnBps: encoded.netReturnBps.toString(),
      meetsThreshold: encoded.meetsThreshold,
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
  const batch = JSON.parse(readFileSync(batchPath, "utf8"));
  const claim = prove(batch, outDir);
  console.log(`proof written to ${outDir}`);
  console.log(
    `agent ${claim.agentId} · ${claim.tradeCount} trades · net ${claim.netReturnBps}bps · ` +
      `claim >= ${claim.thresholdBps}bps · ${claim.meetsThreshold ? "PROVABLE" : "below threshold"}`,
  );
}
