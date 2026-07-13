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

function scaleToInt(value, field) {
  const scaled = Math.round(Number(value) * SCALE);
  if (!Number.isFinite(scaled)) throw new Error(`${field} is not finite`);
  return BigInt(scaled);
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

  const netReturnBps = (totalPnl * 10_000n) / totalNotional;
  return {
    count: trades.length,
    agentId: normalizeField(agentId),
    thresholdBps: threshold,
    netPnl,
    notional,
    netReturnBps,
    meetsThreshold: totalPnl * 10_000n >= threshold * totalNotional,
  };
}

function normalizeField(value) {
  const hex = typeof value === "string" && value.startsWith("0x") ? value : `0x${BigInt(value).toString(16)}`;
  return hex.toLowerCase();
}

/// Render encoded inputs as a Prover.toml body. A blinding must be supplied by the caller (a fresh
/// random field per proof) so the commitment does not leak the trades to a guessing attacker.
export function toProverToml({ encoded, blinding }) {
  const arr = (xs) => `[${xs.map((x) => `"${x.toString()}"`).join(", ")}]`;
  return [
    `count = "${encoded.count}"`,
    `blinding = "${normalizeField(blinding)}"`,
    `agent_id = "${encoded.agentId}"`,
    `threshold_bps = "${encoded.thresholdBps.toString()}"`,
    `trade_count = "${encoded.count}"`,
    `net_pnl = ${arr(encoded.netPnl)}`,
    `notional = ${arr(encoded.notional)}`,
    "",
  ].join("\n");
}
