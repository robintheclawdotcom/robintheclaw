// Canonical trade-record encoding. A record is one leg-balanced trade: a spot position on
// Uniswap v4 and the offsetting perp on Lighter, plus the basis captured and the residual delta.
// The leaf is keccak256 over a fixed-order ABI encoding, so the same record always hashes to the
// same leaf and anyone recomputes it independently. All prices/amounts are integers (scaled) to
// keep hashing deterministic; no floats enter the hash.

import { keccak256, encodeAbiParameters, parseAbiParameters, getAddress } from "viem";

export const SIDE = { open: 0, close: 1 };

// scale helpers: callers pass human numbers, we fix the on-wire integer scale
export const E8 = (x) => BigInt(Math.round(x * 1e8));
export const E18 = (x) => BigInt(Math.round(x * 1e6)) * 10n ** 12n; // 6 sig-figs into 18dp
export const BPS_E2 = (x) => BigInt(Math.round(x * 100));

const LEAF_TYPES = parseAbiParameters(
  "uint32 seq, uint64 ts, string symbol, uint8 side, address spotToken, uint256 spotAmount, uint256 spotPriceE8, uint32 perpMarketId, int256 perpSizeE8, uint256 perpMarkE8, int32 basisBpsE2, int256 netDeltaE8",
);

/// Build a normalized record from human inputs. Throws on anything that would make the leaf
/// ambiguous (unknown side, bad address).
export function record(r) {
  if (!(r.side in SIDE)) throw new Error(`bad side: ${r.side}`);
  return {
    seq: Number(r.seq),
    ts: Number(r.ts),
    symbol: String(r.symbol),
    side: SIDE[r.side],
    spotToken: getAddress(r.spotToken),
    spotAmount: E18(r.spotAmount),
    spotPriceE8: E8(r.spotPriceUsd),
    perpMarketId: Number(r.perpMarketId),
    perpSizeE8: E8(r.perpSize),
    perpMarkE8: E8(r.perpMark),
    basisBpsE2: BPS_E2(r.basisBps),
    netDeltaE8: E8(r.netDeltaUsd),
  };
}

export function leaf(rec) {
  const encoded = encodeAbiParameters(LEAF_TYPES, [
    rec.seq, BigInt(rec.ts), rec.symbol, rec.side, rec.spotToken, rec.spotAmount,
    rec.spotPriceE8, rec.perpMarketId, rec.perpSizeE8, rec.perpMarkE8, rec.basisBpsE2, rec.netDeltaE8,
  ]);
  return keccak256(encoded);
}
