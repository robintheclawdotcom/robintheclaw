// End-to-end demo and self-test of the provable pipeline. Builds a small batch of (synthetic but
// realistically shaped) delta-neutral trades, anchors the root, then shows an independent verifier
// recomputing it, catching a tampered record, and checking a single-trade inclusion proof.
// Exits non-zero on any assertion failure so it doubles as a test.

import assert from "node:assert/strict";
import { prove, verify, proveInclusion, checkInclusion } from "./batch.mjs";

// three legs of a delta-neutral run: open NVDA, open TSLA, close NVDA. Numbers echo a live scan.
const trades = [
  {
    seq: 0, ts: 1_752_340_000_000, symbol: "NVDA", side: "open",
    spotToken: "0xd0601CE157Db5bdC3162BbaC2a2C8aF5320D9EEC",
    spotAmount: 47.0, spotPriceUsd: 212.338,
    perpMarketId: 130, perpSize: -47.0, perpMark: 212.177,
    basisBps: -7.6, netDeltaUsd: 0.0,
  },
  {
    seq: 1, ts: 1_752_340_060_000, symbol: "TSLA", side: "open",
    spotToken: "0x322F0929c4625eD5bAd873c95208D54E1c003b2d",
    spotAmount: 24.5, spotPriceUsd: 407.430,
    perpMarketId: 131, perpSize: -24.5, perpMark: 407.500,
    basisBps: 1.7, netDeltaUsd: 0.0,
  },
  {
    seq: 2, ts: 1_752_343_600_000, symbol: "NVDA", side: "close",
    spotToken: "0xd0601CE157Db5bdC3162BbaC2a2C8aF5320D9EEC",
    spotAmount: 47.0, spotPriceUsd: 212.510,
    perpMarketId: 130, perpSize: 47.0, perpMark: 212.505,
    basisBps: -0.2, netDeltaUsd: 0.0,
  },
];

const { root, anchor, leaves } = prove(trades, 1);
console.log(`batch of ${anchor.tradeCount} trades`);
console.log(`root      ${root}`);
console.log(`anchor()  sequence=${anchor.sequence} tradeCount=${anchor.tradeCount}`);

// 1. an independent verifier recomputes the root from the published records
const good = verify(trades, root);
assert.equal(good.ok, true, "honest batch must verify");
console.log(`\nverify(published records) -> ${good.ok ? "MATCH" : "FAIL"}  (${good.reason})`);

// 2. tamper: nudge one filled price. the leaf changes, the root changes, verification fails
const tampered = structuredClone(trades);
tampered[0].spotPriceUsd = 210.0; // claim a better fill than actually happened
const bad = verify(tampered, root);
assert.equal(bad.ok, false, "tampered batch must fail against the anchored root");
console.log(`verify(tampered records)  -> ${bad.ok ? "MATCH" : "REJECTED"}  (${bad.reason})`);

// 3. inclusion: prove trade #1 is in the batch without revealing the others
const inc = proveInclusion(trades, 1);
assert.equal(inc.root, root, "inclusion proof root must equal batch root");
assert.equal(checkInclusion(inc.leaf, inc.path, root), true, "valid inclusion proof must check");
assert.equal(checkInclusion(leaves[0], inc.path, root), false, "wrong leaf must not check");
console.log(`inclusion proof (trade #1) -> ${checkInclusion(inc.leaf, inc.path, root) ? "VALID" : "FAIL"} (path ${inc.path.length} nodes)`);

console.log("\nall assertions passed");
