# signal

Read-only measurement for the agent's profitability research. It tests whether a measured basis is
real, liquid, and persistent enough to survive execution costs before any execution code is
written. No keys, no on-chain writes, no orders.

## Two steps

```bash
# 1. discover the Uniswap v4 pool keys once (writes data/poolkeys.json)
node src/discover.mjs

# 2. scan the cross-venue basis (Uniswap v4 spot vs Lighter perp)
node src/spot.mjs
```

`discover.mjs` reads the PoolManager's `Initialize` events to recover every Stock-Token/USDG
pool's exact key (fee, tickSpacing, hooks). Anyone can permissionlessly create a v4 pool, so most
names have several, including spam pools at absurd fees (90%+) with no liquidity.

`spot.mjs` reads each discovered pool's price and liquidity from StateView, picks the deepest pool
per name, and reports the basis against the Lighter perp mark. Pools below a tenth of the universe
median liquidity are flagged `THIN`: a wide basis on a shallow pool is a stale mark, not a
capturable spread. Every observation appends to `data/xbasis-YYYYMMDD.jsonl`.

`basis.mjs` is the older perp-only view (perp mark vs the perp's own index); kept as a fast
sanity check that needs no on-chain reads.

## Reading the output

- **High-confidence** signals are names that are deep *and* tight: their spot and perp agree
  closely and the pool has real liquidity (NVDA, TSLA, META have been in the low single-digit bps
  at 1e15+ liquidity).
- **Deepest-pool is a heuristic, not truth.** A pool can hold liquidity at a stale price, so a
  wide basis on an otherwise-deep pool (SPY, AMZN have shown 100-250bps) needs per-pool validation
  before it is treated as tradable. Aggregating a liquidity-weighted price across a name's pools,
  and weighting by recent swap activity, is the next refinement.

## What to watch

The thesis is that the basis is tight during U.S. market hours and widens off-hours and on
weekends, when spot venues thin out. A single reading proves the pipeline; a series proves or
kills the edge. Schedule it to build the series:

```bash
# every 15 minutes via cron
*/15 * * * * cd /path/to/robin-the-claw/signal && /usr/bin/node src/spot.mjs >> scan.log 2>&1
```

## Not yet

- Funding: model Lighter's own charged funding. The public `funding-rates` endpoint returns
  external venue reference rates, not Lighter's, so the raw references are stored for later rather
  than turned into a carry figure here.
- Liquidity-weighted spot across a name's pools, and depth in USD terms (v4 liquidity is raw L,
  not a dollar figure), to size how much basis is actually capturable.
