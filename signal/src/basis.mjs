#!/usr/bin/env node
// Read-only basis scanner. For each name in the tradable universe it reads the Lighter perp
// (mark, index, open interest) and reports the perp's premium/discount to its index in bps.
// No keys, no writes on-chain, no execution. This is the measurement step: confirm the basis
// exists and watch how it moves before building anything that touches money.
//
// Funding is intentionally NOT annualized into a headline number here. The public funding-rates
// endpoint returns external venue reference rates (binance, etc.), not Lighter's own charged
// funding, so the raw references are stored to the JSONL for later modeling rather than shown
// as a carry figure we can't yet stand behind.

import { readFileSync, mkdirSync, appendFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const cfg = JSON.parse(readFileSync(join(root, "config", "addresses.json"), "utf8"));
const API = cfg.perp.api;
const UNIVERSE = new Set(cfg.universe);

const bps = (x) => x * 1e4;

async function getJSON(path) {
  const r = await fetch(`${API}${path}`, { signal: AbortSignal.timeout(20000) });
  if (!r.ok) throw new Error(`${path} -> ${r.status}`);
  return r.json();
}

async function scan() {
  const [details, fundings] = await Promise.all([
    getJSON("/api/v1/orderBookDetails"),
    getJSON("/api/v1/funding-rates").catch(() => null),
  ]);

  const refsByMarket = new Map();
  for (const f of fundings?.funding_rates ?? []) {
    if (!refsByMarket.has(f.market_id)) refsByMarket.set(f.market_id, []);
    refsByMarket.get(f.market_id).push({ exchange: f.exchange, rate: Number(f.rate) });
  }

  const rows = [];
  for (const d of details.order_book_details ?? []) {
    if (d.market_type !== "perp" || d.status !== "active") continue;
    if (!UNIVERSE.has(d.symbol)) continue;

    const mark = Number(d.mark_price);
    const index = Number(d.index_price);
    if (!(mark > 0 && index > 0)) continue;

    rows.push({
      symbol: d.symbol,
      marketId: d.market_id,
      mark,
      index,
      basisBps: bps((mark - index) / index),
      openInterest: Number(d.open_interest),
      maintMarginFrac: d.maintenance_margin_fraction,
      fundingRefs: refsByMarket.get(d.market_id) ?? [],
    });
  }

  rows.sort((a, b) => Math.abs(b.basisBps) - Math.abs(a.basisBps));
  return rows;
}

function fmt(rows) {
  const ts = new Date().toISOString();
  const absAvg = rows.reduce((s, r) => s + Math.abs(r.basisBps), 0) / (rows.length || 1);
  const head = `[${ts}] universe=${UNIVERSE.size} live=${rows.length} avg|basis|=${absAvg.toFixed(1)}bps`;
  const lines = rows.map((r) => {
    const b = `${r.basisBps >= 0 ? "+" : ""}${r.basisBps.toFixed(1)}`;
    return `  ${r.symbol.padEnd(6)} mark=${r.mark.toFixed(3).padStart(10)} index=${r.index.toFixed(3).padStart(10)} basis=${b.padStart(7)}bps  OI=${r.openInterest.toFixed(0).padStart(8)}`;
  });
  return [head, ...lines].join("\n");
}

function persist(rows) {
  const dir = join(root, "signal", "data");
  mkdirSync(dir, { recursive: true });
  const day = new Date().toISOString().slice(0, 10);
  const file = join(dir, `basis-${day}.jsonl`);
  const ts = Date.now();
  for (const r of rows) appendFileSync(file, JSON.stringify({ ts, ...r }) + "\n");
  return file;
}

const rows = await scan();
console.log(fmt(rows));
const file = persist(rows);
console.log(`\nappended ${rows.length} rows -> ${file.replace(root + "/", "")}`);
