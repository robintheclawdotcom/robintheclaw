#!/usr/bin/env node
// Cross-venue basis scanner: the real signal. For each name it reads the Uniswap v4 AMM spot
// price on Robinhood Chain (on-chain, 24/7) and the Lighter perp mark, and reports the basis
// between them. Stock-Token pools are keyed by a PoolKey whose fee tier we discover by probing
// the common combos (hooks = 0) against StateView; discovered keys are cached to disk so reruns
// are fast. Names whose pool uses custom hooks are reported as such, not guessed.
//
// Read-only: no keys, no writes, no execution. Appends every observation to a JSONL series.

import { readFileSync, writeFileSync, existsSync, mkdirSync, appendFileSync } from "node:fs";
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
const CANDIDATES = [
  { fee: 100, tickSpacing: 1 },
  { fee: 500, tickSpacing: 10 },
  { fee: 3000, tickSpacing: 60 },
  { fee: 10000, tickSpacing: 200 },
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

function poolId(c0, c1, fee, tickSpacing) {
  return keccak256(encodeAbiParameters(
    parseAbiParameters("address,address,uint24,int24,address"),
    [c0, c1, fee, tickSpacing, ZERO_HOOKS],
  ));
}

function usdPrice(sqrtPriceX96, dec0, dec1, usdgIsCurrency0) {
  const sp = Number(sqrtPriceX96) / 2 ** 96;
  const oneForZero = sp * sp * 10 ** (dec0 - dec1); // currency1 per currency0
  return usdgIsCurrency0 ? 1 / oneForZero : oneForZero;
}

async function slot0(id) {
  try {
    const [sqrtPriceX96] = await client.readContract({
      address: STATE_VIEW, abi: stateViewAbi, functionName: "getSlot0", args: [id],
    });
    return sqrtPriceX96;
  } catch {
    return 0n;
  }
}

async function spot(symbol) {
  const token = getAddress(cfg.stockTokens[symbol]);
  const [c0, c1] = token.toLowerCase() < USDG.toLowerCase() ? [token, USDG] : [USDG, token];
  const usdgIsCurrency0 = c0.toLowerCase() === USDG.toLowerCase();
  const [d0, d1] = await Promise.all([decimals(c0), decimals(c1)]);

  const tryKey = async (fee, tickSpacing) => {
    const id = poolId(c0, c1, fee, tickSpacing);
    const sq = await slot0(id);
    if (!sq || sq === 0n) return null;
    const liq = await client
      .readContract({ address: STATE_VIEW, abi: stateViewAbi, functionName: "getLiquidity", args: [id] })
      .catch(() => 0n);
    return { price: usdPrice(sq, d0, d1, usdgIsCurrency0), fee, liquidity: liq };
  };

  if (cache[symbol]) {
    const hit = await tryKey(cache[symbol].fee, cache[symbol].tickSpacing);
    if (hit) return hit;
  }
  for (const { fee, tickSpacing } of CANDIDATES) {
    const hit = await tryKey(fee, tickSpacing);
    if (hit) {
      cache[symbol] = { fee, tickSpacing };
      return hit;
    }
  }
  return null;
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

mkdirSync(join(root, "signal", "data"), { recursive: true });
writeFileSync(cacheFile, JSON.stringify(cache, null, 2));

const live = rows.filter((r) => r.basisBps !== null);
// A pool whose liquidity is under a tenth of the universe median is too thin to trust: its wide
// basis is a stale mark, not a capturable spread. Flag it so it isn't read as a real signal.
const liqs = live.map((r) => Number(r.liquidity)).filter((x) => x > 0).sort((a, b) => a - b);
const median = liqs.length ? liqs[Math.floor(liqs.length / 2)] : 0;
const thinBelow = median * 0.1;
const isThin = (r) => Number(r.liquidity) < thinBelow;
for (const r of live) r.thin = isThin(r);

const tradable = live.filter((r) => !r.thin);
const absAvg = tradable.reduce((s, r) => s + Math.abs(r.basisBps), 0) / (tradable.length || 1);
console.log(`Robinhood Chain block ${bn} · cross-venue (Uniswap v4 spot vs Lighter perp)`);
console.log(`universe=${names.length} paired=${live.length} tradable=${tradable.length} avg|basis|(tradable)=${absAvg.toFixed(1)}bps\n`);
for (const r of rows) {
  if (r.basisBps === null) {
    console.log(`  ${r.symbol.padEnd(6)} ${r.spot === null ? "no hooks-free v4 pool" : "no perp"}`);
  } else {
    const b = `${r.basisBps >= 0 ? "+" : ""}${r.basisBps.toFixed(1)}`;
    const liqAbbr = Number(r.liquidity).toExponential(1);
    const tag = r.thin ? "  THIN" : "";
    console.log(`  ${r.symbol.padEnd(6)} spot=$${r.spot.toFixed(3).padStart(9)} perp=${r.mark.toFixed(3).padStart(9)} basis=${b.padStart(7)}bps (fee ${r.fee}, liq ${liqAbbr})${tag}`);
  }
}

const day = new Date().toISOString().slice(0, 10);
const ts = Date.now();
const file = join(root, "signal", "data", `xbasis-${day}.jsonl`);
for (const r of live) appendFileSync(file, JSON.stringify({ ts, block: Number(bn), ...r }) + "\n");
console.log(`\nappended ${live.length} rows -> ${file.replace(root + "/", "")}`);
