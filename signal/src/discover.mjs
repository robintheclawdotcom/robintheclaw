#!/usr/bin/env node
// Pool discovery. The brute-force spot probe only finds pools with no hooks; Stock-Token pools
// that use a custom hook are invisible to it. Uniswap v4's PoolManager emits Initialize with the
// full PoolKey (fee, tickSpacing, hooks) and indexed currencies, so one topic-filtered getLogs
// per name recovers the exact key. Results are written to the shared poolkeys cache that the
// spot scanner reads, so this runs once (or when the universe changes), not every scan.

import { readFileSync, writeFileSync, existsSync, mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { createPublicClient, http, getAddress } from "viem";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const cfg = JSON.parse(readFileSync(join(root, "config", "addresses.json"), "utf8"));
const cacheFile = join(root, "signal", "data", "poolkeys.json");

const client = createPublicClient({ transport: http(cfg.chain.mainnet.rpc) });
const POOL_MANAGER = getAddress(cfg.uniswapV4.PoolManager);
const USDG = getAddress(cfg.deployment.mainnet.asset);

const initEvent = {
  type: "event",
  name: "Initialize",
  inputs: [
    { name: "id", type: "bytes32", indexed: true },
    { name: "currency0", type: "address", indexed: true },
    { name: "currency1", type: "address", indexed: true },
    { name: "fee", type: "uint24", indexed: false },
    { name: "tickSpacing", type: "int24", indexed: false },
    { name: "hooks", type: "address", indexed: false },
    { name: "sqrtPriceX96", type: "uint160", indexed: false },
    { name: "tick", type: "int24", indexed: false },
  ],
};

async function discover(symbol) {
  const tokenAddress = cfg.stockTokens[symbol];
  if (!tokenAddress) return { symbol, error: "unknown symbol" };
  const token = getAddress(tokenAddress);
  const [c0, c1] = token.toLowerCase() < USDG.toLowerCase() ? [token, USDG] : [USDG, token];
  let logs;
  try {
    logs = await client.getLogs({
      address: POOL_MANAGER,
      event: initEvent,
      args: { currency0: c0, currency1: c1 },
      fromBlock: 0n,
      toBlock: "latest",
    });
  } catch (e) {
    return { symbol, error: e.shortMessage || e.message };
  }
  if (!logs.length) return { symbol, found: 0 };
  // multiple fee tiers may exist; keep them all, the scanner picks by liquidity
  const keys = logs.map((l) => ({
    fee: Number(l.args.fee),
    tickSpacing: Number(l.args.tickSpacing),
    hooks: getAddress(l.args.hooks),
  }));
  return { symbol, found: keys.length, keys };
}

const names = process.argv.slice(2).length ? process.argv.slice(2) : cfg.universe;
const cache = existsSync(cacheFile) ? JSON.parse(readFileSync(cacheFile, "utf8")) : {};
let failed = false;

for (const s of names) {
  const r = await discover(s);
  if (r.error) {
    console.error(`  ${s.padEnd(6)} discovery failed: ${r.error}`);
    failed = true;
    continue;
  }
  if (!r.found) {
    console.log(`  ${s.padEnd(6)} no Initialize event (no USDG pool)`);
    continue;
  }
  cache[s] = { keys: r.keys };
  const desc = r.keys.map((k) => `fee ${k.fee}/${k.hooks === "0x0000000000000000000000000000000000000000" ? "no-hook" : k.hooks.slice(0, 10)}`).join(", ");
  console.log(`  ${s.padEnd(6)} ${r.found} pool(s): ${desc}`);
}

mkdirSync(dirname(cacheFile), { recursive: true });
writeFileSync(cacheFile, JSON.stringify(cache, null, 2));
console.log(`\ncache -> ${cacheFile.replace(root + "/", "")}`);
if (failed) process.exitCode = 1;
