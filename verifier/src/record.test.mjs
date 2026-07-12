import assert from "node:assert/strict";
import { prove, proveInclusion, verify } from "./batch.mjs";

const record = {
  seq: 0,
  ts: 1_783_886_400_000,
  symbol: "TEST",
  side: "open",
  spotToken: "0x5AaeB5a094F0EA6dBE685B2b69e56c112e4568BF",
  spotAmount: 1,
  spotPriceUsd: 1,
  perpMarketId: 0,
  perpSize: -1,
  perpMark: 1,
  basisBps: 0,
  netDeltaUsd: 0,
};

assert.throws(() => prove([], 1), /empty batch/);
assert.throws(() => prove([{ ...record, spotAmount: 0 }], 1), /positive/);
assert.throws(() => prove([{ ...record, seq: 2 ** 32 }], 1), /seq is out of range/);
assert.throws(() => prove([{ ...record, perpMark: Number.NaN }], 1), /perpMark must be finite/);
assert.throws(() => proveInclusion([record], 1), /proof index is out of range/);

const { root } = prove([record], 1);
assert.equal(verify([record], root).ok, true);
console.log("record validation assertions passed");
