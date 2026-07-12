// Canonical trade-record encoding. A record is one leg-balanced trade: a spot position on
// Uniswap v4 and the offsetting perp on Lighter, plus the basis captured and the residual delta.
// The leaf is keccak256 over a fixed-order ABI encoding, so the same record always hashes to the
// same leaf and anyone recomputes it independently. All prices/amounts are integers (scaled) to
// keep hashing deterministic; no floats enter the hash.

import { keccak256, encodeAbiParameters, parseAbiParameters, getAddress } from "viem";

export const SIDE = { open: 0, close: 1 };

function finite(value, field) {
  const number = Number(value);
  if (!Number.isFinite(number)) throw new Error(`${field} must be finite`);
  return number;
}

function integer(value, field, max) {
  const number = finite(value, field);
  if (!Number.isSafeInteger(number) || number < 0 || number > max) {
    throw new Error(`${field} is out of range`);
  }
  return number;
}

function scaled(value, scale, field) {
  return BigInt(Math.round(finite(value, field) * scale));
}

export const E8 = (value) => scaled(value, 1e8, "value");
export const E18 = (value) => scaled(value, 1e6, "value") * 10n ** 12n;
export const BPS_E2 = (value) => scaled(value, 100, "value");

const LEAF_TYPES = parseAbiParameters(
  "uint32 seq, uint64 ts, string symbol, uint8 side, address spotToken, uint256 spotAmount, uint256 spotPriceE8, uint32 perpMarketId, int256 perpSizeE8, uint256 perpMarkE8, int32 basisBpsE2, int256 netDeltaE8",
);

/// Build a normalized record from human inputs. Throws on anything that would make the leaf
/// ambiguous (unknown side, bad address).
export function record(r) {
  if (!r || typeof r !== "object") throw new Error("record must be an object");
  if (!(r.side in SIDE)) throw new Error(`bad side: ${r.side}`);
  const symbol = String(r.symbol ?? "").trim();
  if (!symbol) throw new Error("symbol is required");

  const spotAmount = finite(r.spotAmount, "spotAmount");
  const spotPriceUsd = finite(r.spotPriceUsd, "spotPriceUsd");
  const perpMark = finite(r.perpMark, "perpMark");
  if (spotAmount <= 0 || spotPriceUsd <= 0 || perpMark <= 0) {
    throw new Error("spot amount and prices must be positive");
  }

  const basisBps = finite(r.basisBps, "basisBps");
  const perpSize = finite(r.perpSize, "perpSize");
  const netDeltaUsd = finite(r.netDeltaUsd, "netDeltaUsd");
  return {
    seq: integer(r.seq, "seq", 2 ** 32 - 1),
    ts: integer(r.ts, "ts", Number.MAX_SAFE_INTEGER),
    symbol,
    side: SIDE[r.side],
    spotToken: getAddress(r.spotToken),
    spotAmount: E18(spotAmount),
    spotPriceE8: E8(spotPriceUsd),
    perpMarketId: integer(r.perpMarketId, "perpMarketId", 2 ** 32 - 1),
    perpSizeE8: E8(perpSize),
    perpMarkE8: E8(perpMark),
    basisBpsE2: BPS_E2(basisBps),
    netDeltaE8: E8(netDeltaUsd),
  };
}

export function leaf(rec) {
  const encoded = encodeAbiParameters(LEAF_TYPES, [
    rec.seq, BigInt(rec.ts), rec.symbol, rec.side, rec.spotToken, rec.spotAmount,
    rec.spotPriceE8, rec.perpMarketId, rec.perpSizeE8, rec.perpMarkE8, rec.basisBpsE2, rec.netDeltaE8,
  ]);
  return keccak256(encoded);
}
