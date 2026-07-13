// Encode a trade batch into the circuit's witness inputs. The circuit proves that an agent's net
// return in basis points over a committed set of trades cleared a public threshold, without
// revealing the trades. This module owns the one place where trades become field-aligned integers,
// so the encoding is defined once and shared by the prover and its tests.

export const MAX_TRADES = 32;
export const PNL_BIAS = 1_000_000_000_000n;
export const MAX_NOTIONAL = 1_000_000_000_000n;
export const MAX_THRESHOLD_BPS = 200_000n;
// PnL and notional are scaled to integer micro-dollars (1e6) before proving, matching the trade
// record's fixed-point convention. No floats enter the witness.
export const SCALE = 1_000_000;
// BN254 scalar field, the order agent_id, blinding, and the commitment live in.
export const FIELD_MODULUS =
  21888242871839275222246405745257275088548364400416034343698204186575808495617n;

function scaleToInt(value, field) {
  // Bounds cap scaled amounts below 2^53, so Number keeps them exact; sub-micro-dollar precision
  // is intentionally dropped and out-of-range values are rejected downstream.
  const scaled = Math.round(Number(value) * SCALE);
  if (!Number.isFinite(scaled)) throw new Error(`${field} is not finite`);
  return BigInt(scaled);
}

/// Parse a field element (agent id, blinding) from a hex or decimal value, rejecting anything that
/// is not a canonical element: non-hex junk, a negative, or a value at or above the field modulus.
/// Passing an out-of-range value straight through would either fail deep in witness solving or, if
/// silently reduced, verify against a different on-chain value.
export function toField(value, field) {
  let v;
  if (typeof value === "bigint") {
    v = value;
  } else if (typeof value === "number" && Number.isInteger(value)) {
    v = BigInt(value);
  } else if (typeof value === "string" && /^(0x[0-9a-fA-F]+|[0-9]+)$/.test(value.trim())) {
    v = BigInt(value.trim());
  } else {
    throw new Error(`${field} is not a valid field element`);
  }
  if (v < 0n) throw new Error(`${field} must be non-negative`);
  if (v >= FIELD_MODULUS) throw new Error(`${field} is at or above the field modulus`);
  return `0x${v.toString(16)}`;
}

/// Turn a batch of { netPnlUsd, notionalUsd } trades plus claim parameters into the exact typed
/// inputs the circuit expects, validating every bound the circuit enforces so a bad batch fails
/// here with a clear message instead of deep inside witness solving.
export function encodeBatch({ agentId, thresholdBps, trades }) {
  if (!Array.isArray(trades) || trades.length === 0) {
    throw new Error("batch needs at least one trade");
  }
  if (trades.length > MAX_TRADES) {
    throw new Error(`batch exceeds ${MAX_TRADES} trades`);
  }
  const threshold = BigInt(thresholdBps);
  if (threshold > MAX_THRESHOLD_BPS || threshold < -MAX_THRESHOLD_BPS) {
    throw new Error("threshold_bps out of range");
  }

  const netPnl = new Array(MAX_TRADES).fill(0n);
  const notional = new Array(MAX_TRADES).fill(0n);
  let totalPnl = 0n;
  let totalNotional = 0n;

  trades.forEach((trade, i) => {
    const pnl = scaleToInt(trade.netPnlUsd, `trades[${i}].netPnlUsd`);
    const notl = scaleToInt(trade.notionalUsd, `trades[${i}].notionalUsd`);
    if (pnl > PNL_BIAS || pnl < -PNL_BIAS) throw new Error(`trades[${i}] pnl out of range`);
    if (notl <= 0n || notl > MAX_NOTIONAL) throw new Error(`trades[${i}] notional out of range`);
    netPnl[i] = pnl;
    notional[i] = notl;
    totalPnl += pnl;
    totalNotional += notl;
  });

  // netReturnBps is a truncated display figure; the accept/reject decision uses the exact
  // division-free form the circuit enforces, so the two never disagree at the boundary.
  const netReturnBps = (totalPnl * 10_000n) / totalNotional;
  return {
    count: trades.length,
    agentId: toField(agentId, "agentId"),
    thresholdBps: threshold,
    netPnl,
    notional,
    netReturnBps,
    meetsThreshold: totalPnl * 10_000n >= threshold * totalNotional,
  };
}

/// Render encoded inputs as a Prover.toml body. The blinding must be a fresh random field per proof
/// so the commitment does not leak the trades to a guessing attacker; a zero blinding is refused
/// because it provides no hiding.
export function toProverToml({ encoded, blinding }) {
  const b = toField(blinding, "blinding");
  if (BigInt(b) === 0n) throw new Error("blinding must be non-zero");
  const arr = (xs) => `[${xs.map((x) => `"${x.toString()}"`).join(", ")}]`;
  return [
    `count = "${encoded.count}"`,
    `blinding = "${b}"`,
    `agent_id = "${encoded.agentId}"`,
    `threshold_bps = "${encoded.thresholdBps.toString()}"`,
    `trade_count = "${encoded.count}"`,
    `net_pnl = ${arr(encoded.netPnl)}`,
    `notional = ${arr(encoded.notional)}`,
    "",
  ].join("\n");
}
