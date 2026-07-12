#!/usr/bin/env node
// Cross-venue basis scanner: the real signal. For each name it reads the Uniswap v4 AMM spot
// price on Robinhood Chain (on-chain, 24/7) and the Lighter perp mark, and reports the basis.
// Pool keys come from the discovery cache (run discover.mjs first); any name with several fee
// tiers is resolved to its deepest pool by liquidity, which discards the permissionless spam
// pools (absurd fees, no liquidity) and keeps the real one. Names below a liquidity floor are
// flagged THIN because a wide basis on a shallow pool is a stale mark, not a capturable spread.
//
// Read-only: no keys, no writes on-chain, no execution. Appends every observation to JSONL.

import { readFileSync, existsSync, mkdirSync, appendFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import {
  createPublicClient, http, encodeAbiParameters, parseAbiParameters, keccak256, getAddress,
} from "viem";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const cfg = JSON.parse(readFileSync(join(root, "config", "addresses.json"), "utf8"));
const cacheFile = join(root, "signal", "data", "poolkeys.json");

const client = createPublicClient({ transport: http(cfg.chain.mainnet.rpc) });
const STATE_VIEW = getAddress(cfg.uniswapV4.StateView);
const USDG = getAddress(cfg.core.USDG);
const ZERO_HOOKS = "0x0000000000000000000000000000000000000000";
// fallback fee/tickSpacing combos for names with no discovery cache entry
const FALLBACK = [
  { fee: 100, tickSpacing: 1, hooks: ZERO_HOOKS },
  { fee: 500, tickSpacing: 10, hooks: ZERO_HOOKS },
  { fee: 3000, tickSpacing: 60, hooks: ZERO_HOOKS },
  { fee: 10000, tickSpacing: 200, hooks: ZERO_HOOKS },
];

const stateViewAbi = [
  {
    name: "getSlot0", type: "function", stateMutability: "view",
    inputs: [{ name: "poolId", type: "bytes32" }],
    outputs: [
      { name: "sqrtPriceX96", type: "uint160" }, { name: "tick", type: "int24" },
      { name: "protocolFee", type: "uint24" }, { name: "lpFee", type: "uint24" },
    ],
  },
  {
    name: "getLiquidity", type: "function", stateMutability: "view",
    inputs: [{ name: "poolId", type: "bytes32" }],
    outputs: [{ name: "liquidity", type: "uint128" }],
  },
];
const decAbi = [{ name: "decimals", type: "function", stateMutability: "view", inputs: [], outputs: [{ type: "uint8" }] }];

const cache = existsSync(cacheFile) ? JSON.parse(readFileSync(cacheFile, "utf8")) : {};
const decCache = {};

async function decimals(token) {
  if (decCache[token] !== undefined) return decCache[token];
  const d = await client.readContract({ address: token, abi: decAbi, functionName: "decimals" }).catch(() => 18);
  decCache[token] = Number(d);
  return decCache[token];
}

function poolId(c0, c1, fee, tickSpacing, hooks) {
  return keccak256(encodeAbiParameters(
    parseAbiParameters("address,address,uint24,int24,address"),
    [c0, c1, fee, tickSpacing, getAddress(hooks)],
  ));
}

function usdPrice(sqrtPriceX96, dec0, dec1, usdgIsCurrency0) {
  const sp = Number(sqrtPriceX96) / 2 ** 96;
  const oneForZero = sp * sp * 10 ** (dec0 - dec1); // currency1 per currency0
  return usdgIsCurrency0 ? 1 / oneForZero : oneForZero;
}

async function poolState(id) {
  try {
    const [[sqrtPriceX96], liquidity] = await Promise.all([
      client.readContract({ address: STATE_VIEW, abi: stateViewAbi, functionName: "getSlot0", args: [id] }),
      client.readContract({ address: STATE_VIEW, abi: stateViewAbi, functionName: "getLiquidity", args: [id] }).catch(() => 0n),
    ]);
    return { sqrtPriceX96, liquidity };
  } catch {
    return { sqrtPriceX96: 0n, liquidity: 0n };
  }
}

// tickSpacing does not affect the poolId sqrt/liquidity read except via the key hash, and the
// discovery cache carries the exact tickSpacing. For fallback keys it is paired with the fee.
async function spot(symbol) {
  const token = getAddress(cfg.stockTokens[symbol]);
  const [c0, c1] = token.toLowerCase() < USDG.toLowerCase() ? [token, USDG] : [USDG, token];
  const usdgIsCurrency0 = c0.toLowerCase() === USDG.toLowerCase();
  const [d0, d1] = await Promise.all([decimals(c0), decimals(c1)]);
  const keys = cache[symbol]?.keys ?? FALLBACK;

  let best = null;
  for (const k of keys) {
    const ts = k.tickSpacing ?? 60;
    const { sqrtPriceX96, liquidity } = await poolState(poolId(c0, c1, k.fee, ts, k.hooks ?? ZERO_HOOKS));
    if (!sqrtPriceX96 || sqrtPriceX96 === 0n) continue;
    if (!best || liquidity > best.liquidity) {
      best = { price: usdPrice(sqrtPriceX96, d0, d1, usdgIsCurrency0), fee: k.fee, liquidity };
    }
  }
  return best;
}

async function perpMarks() {
  const r = await fetch(`${cfg.perp.api}/api/v1/orderBookDetails`, { signal: AbortSignal.timeout(20000) });
  const j = await r.json();
  const m = new Map();
  for (const d of j.order_book_details ?? []) {
    if (d.market_type === "perp" && d.status === "active") m.set(d.symbol, +d.mark_price);
  }
  return m;
}

async function mapLimit(items, limit, fn) {
  const out = [];
  for (let i = 0; i < items.length; i += limit) {
    out.push(...(await Promise.all(items.slice(i, i + limit).map(fn))));
  }
  return out;
}

const names = process.argv.slice(2).length ? process.argv.slice(2) : cfg.universe;
const marks = await perpMarks();
const bn = await client.getBlockNumber();

const rows = (await mapLimit(names, 4, async (s) => {
  const sp = await spot(s).catch(() => null);
  const mark = marks.get(s);
  const liquidity = sp?.liquidity != null ? sp.liquidity.toString() : null;
  if (!sp || !mark) return { symbol: s, spot: sp?.price ?? null, mark: mark ?? null, basisBps: null, fee: sp?.fee ?? null, liquidity };
  return { symbol: s, spot: sp.price, mark, fee: sp.fee, liquidity, basisBps: ((mark - sp.price) / sp.price) * 1e4 };
})).sort((a, b) => Math.abs(b.basisBps ?? -1) - Math.abs(a.basisBps ?? -1));

const live = rows.filter((r) => r.basisBps !== null);
// A pool whose liquidity is under a tenth of the universe median is too thin to trust: its wide
// basis is a stale mark, not a capturable spread. Flag it so it isn't read as a real signal.
const liqs = live.map((r) => Number(r.liquidity)).filter((x) => x > 0).sort((a, b) => a - b);
const median = liqs.length ? liqs[Math.floor(liqs.length / 2)] : 0;
const thinBelow = median * 0.1;
for (const r of live) r.thin = Number(r.liquidity) < thinBelow;

const tradable = live.filter((r) => !r.thin);
const absAvg = tradable.reduce((s, r) => s + Math.abs(r.basisBps), 0) / (tradable.length || 1);
console.log(`Robinhood Chain block ${bn} · cross-venue (Uniswap v4 spot vs Lighter perp)`);
console.log(`universe=${names.length} paired=${live.length} tradable=${tradable.length} avg|basis|(tradable)=${absAvg.toFixed(1)}bps\n`);
for (const r of rows) {
  if (r.basisBps === null) {
    console.log(`  ${r.symbol.padEnd(6)} ${r.spot === null ? "no v4 pool" : "no perp"}`);
  } else {
    const b = `${r.basisBps >= 0 ? "+" : ""}${r.basisBps.toFixed(1)}`;
    const liqAbbr = Number(r.liquidity).toExponential(1);
    const tag = r.thin ? "  THIN" : "";
    console.log(`  ${r.symbol.padEnd(6)} spot=$${r.spot.toFixed(3).padStart(9)} perp=${r.mark.toFixed(3).padStart(9)} basis=${b.padStart(7)}bps (fee ${r.fee}, liq ${liqAbbr})${tag}`);
  }
}

mkdirSync(join(root, "signal", "data"), { recursive: true });
const day = new Date().toISOString().slice(0, 10);
const ts = Date.now();
const file = join(root, "signal", "data", `xbasis-${day}.jsonl`);
for (const r of live) appendFileSync(file, JSON.stringify({ ts, block: Number(bn), ...r }) + "\n");
console.log(`\nappended ${live.length} rows -> ${file.replace(root + "/", "")}`);
