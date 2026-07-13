import { test } from "node:test";
import assert from "node:assert/strict";
import {
  encodeBatch,
  toProverToml,
  toField,
  MAX_TRADES,
  SCALE,
  FIELD_MODULUS,
  MAX_THRESHOLD_BPS,
  PNL_BIAS,
  MAX_NOTIONAL,
} from "./encode.mjs";

const agentId = "0xabcd";

test("encodes net return in basis points", () => {
  const encoded = encodeBatch({
    agentId,
    thresholdBps: 100,
    trades: [
      { netPnlUsd: 30, notionalUsd: 1000 },
      { netPnlUsd: -10, notionalUsd: 1000 },
      { netPnlUsd: 25, notionalUsd: 1000 },
    ],
  });
  // 45 / 3000 = 150 bps
  assert.equal(encoded.netReturnBps, 150n);
  assert.equal(encoded.meetsThreshold, true);
  assert.equal(encoded.count, 3);
});

test("flags a batch that misses its threshold", () => {
  const encoded = encodeBatch({
    agentId,
    thresholdBps: 200,
    trades: [{ netPnlUsd: 30, notionalUsd: 1000 }],
  });
  assert.equal(encoded.netReturnBps, 300n);
  assert.equal(encoded.meetsThreshold, true);
  const tight = encodeBatch({
    agentId,
    thresholdBps: 400,
    trades: [{ netPnlUsd: 30, notionalUsd: 1000 }],
  });
  assert.equal(tight.meetsThreshold, false);
});

test("supports a negative threshold as a max-loss claim", () => {
  const encoded = encodeBatch({
    agentId,
    thresholdBps: -100,
    trades: [
      { netPnlUsd: -5, notionalUsd: 1000 },
      { netPnlUsd: -3, notionalUsd: 1000 },
    ],
  });
  assert.equal(encoded.netReturnBps, -40n);
  assert.equal(encoded.meetsThreshold, true); // -40 >= -100
});

test("pads the arrays to the fixed circuit width", () => {
  const encoded = encodeBatch({
    agentId,
    thresholdBps: 0,
    trades: [{ netPnlUsd: 1, notionalUsd: 1000 }],
  });
  assert.equal(encoded.netPnl.length, MAX_TRADES);
  assert.equal(encoded.notional.length, MAX_TRADES);
  assert.equal(encoded.netPnl[1], 0n);
  assert.equal(encoded.notional[1], 0n);
});

test("scales dollars to integer micro-dollars", () => {
  const encoded = encodeBatch({
    agentId,
    thresholdBps: 0,
    trades: [{ netPnlUsd: 1.5, notionalUsd: 100 }],
  });
  assert.equal(encoded.netPnl[0], BigInt(1.5 * SCALE));
  assert.equal(encoded.notional[0], BigInt(100 * SCALE));
});

test("rejects an empty batch", () => {
  assert.throws(() => encodeBatch({ agentId, thresholdBps: 0, trades: [] }), /at least one trade/);
});

test("rejects more than the maximum trades", () => {
  const trades = Array.from({ length: MAX_TRADES + 1 }, () => ({ netPnlUsd: 1, notionalUsd: 1000 }));
  assert.throws(() => encodeBatch({ agentId, thresholdBps: 0, trades }), /exceeds 32 trades/);
});

test("rejects a non-positive notional", () => {
  assert.throws(
    () => encodeBatch({ agentId, thresholdBps: 0, trades: [{ netPnlUsd: 1, notionalUsd: 0 }] }),
    /notional out of range/,
  );
});

test("rejects an out-of-range threshold", () => {
  assert.throws(
    () => encodeBatch({ agentId, thresholdBps: 300000, trades: [{ netPnlUsd: 1, notionalUsd: 1000 }] }),
    /threshold_bps out of range/,
  );
});

test("renders a Prover.toml with a blinding", () => {
  const encoded = encodeBatch({ agentId, thresholdBps: 100, trades: [{ netPnlUsd: 1, notionalUsd: 1000 }] });
  const toml = toProverToml({ encoded, blinding: "0x55" });
  assert.match(toml, /blinding = "0x55"/);
  assert.match(toml, /agent_id = "0xabcd"/);
  assert.match(toml, /threshold_bps = "100"/);
  assert.match(toml, /trade_count = "1"/);
});

test("toField normalizes hex, decimal, and numeric inputs", () => {
  assert.equal(toField("0xABCD", "x"), "0xabcd");
  assert.equal(toField("255", "x"), "0xff");
  assert.equal(toField(255, "x"), "0xff");
  assert.equal(toField(255n, "x"), "0xff");
});

test("toField rejects malformed, negative, and oversized values", () => {
  assert.throws(() => toField("0xZZ", "x"), /not a valid field element/);
  assert.throws(() => toField("nope", "x"), /not a valid field element/);
  assert.throws(() => toField(-5, "x"), /non-negative/);
  assert.throws(() => toField(FIELD_MODULUS, "x"), /at or above the field modulus/);
  assert.doesNotThrow(() => toField(FIELD_MODULUS - 1n, "x"));
});

test("toProverToml refuses a zero blinding", () => {
  const encoded = encodeBatch({ agentId, thresholdBps: 100, trades: [{ netPnlUsd: 1, notionalUsd: 1000 }] });
  assert.throws(() => toProverToml({ encoded, blinding: "0x0" }), /blinding must be non-zero/);
});

test("accepts the threshold at its exact bound and rejects one past it", () => {
  const trades = [{ netPnlUsd: 1, notionalUsd: 1000 }];
  assert.doesNotThrow(() => encodeBatch({ agentId, thresholdBps: MAX_THRESHOLD_BPS, trades }));
  assert.doesNotThrow(() => encodeBatch({ agentId, thresholdBps: -MAX_THRESHOLD_BPS, trades }));
  assert.throws(() => encodeBatch({ agentId, thresholdBps: MAX_THRESHOLD_BPS + 1n, trades }), /out of range/);
  assert.throws(() => encodeBatch({ agentId, thresholdBps: -(MAX_THRESHOLD_BPS + 1n), trades }), /out of range/);
});

test("accepts per-trade values at their exact bounds", () => {
  const maxPnlUsd = Number(PNL_BIAS / BigInt(SCALE));
  const maxNotionalUsd = Number(MAX_NOTIONAL / BigInt(SCALE));
  const encoded = encodeBatch({
    agentId,
    thresholdBps: 0,
    trades: [{ netPnlUsd: maxPnlUsd, notionalUsd: maxNotionalUsd }],
  });
  assert.equal(encoded.netPnl[0], PNL_BIAS);
  assert.equal(encoded.notional[0], MAX_NOTIONAL);
});
