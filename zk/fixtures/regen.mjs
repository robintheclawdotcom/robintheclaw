#!/usr/bin/env node
// Regenerate the committed golden fixtures by running the prover CLI on the batch inputs under
// fixtures/batches/. The proof the Solidity test consumes is therefore real CLI output, not a
// hand-made artifact, so the CLI to on-chain seam stays honest. Run from anywhere:
//   node fixtures/regen.mjs
// The proof bytes may differ run to run (proving is randomized); the public parameters that the
// test pins (agent, threshold, trade count, commitment) are deterministic for fixed inputs.

import { mkdtempSync, readFileSync, writeFileSync, copyFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { prove } from "../prover/src/prove.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const contractsFixtures = resolve(here, "..", "contracts", "fixtures");

const cases = [
  { batch: "positive.json", proof: "proof.hex" },
  { batch: "negative.json", proof: "proof-negative.hex" },
];

const summary = {};
for (const { batch, proof } of cases) {
  const input = JSON.parse(readFileSync(join(here, "batches", batch), "utf8"));
  const work = mkdtempSync(join(tmpdir(), "pnl-fixture-"));
  try {
    const claim = prove(input, work);
    copyFileSync(join(work, "proof.hex"), join(contractsFixtures, proof));
    summary[batch] = claim;
    console.log(
      `${batch}: agent ${claim.agentId} threshold ${claim.thresholdBps} count ${claim.tradeCount} ` +
        `net ${claim.netReturnBps}bps commitment ${claim.commitment}`,
    );
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
}
writeFileSync(join(here, "fixtures.json"), `${JSON.stringify(summary, null, 2)}\n`);
console.log(`\nwrote proofs to ${contractsFixtures} and fixtures.json`);
