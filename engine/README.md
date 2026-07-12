# engine

The decision engine. It turns a cross-venue basis observation (Uniswap v4 spot vs Lighter perp)
into a sized, delta-neutral order plan, bounded by portfolio risk limits. Pure logic: no chain
client, no network, no database. The same inputs always produce the same plan, so every decision
is reproducible and can be replayed against the on-chain record.

```bash
cargo test
```

## Replay a decision

The CLI takes an immutable JSON input and returns either an approved two-leg plan or a structured
decline. It never talks to a chain, exchange, or wallet.

```bash
cargo run --bin plan -- fixtures/plan-input.json
```

## The pipeline

`plan_trade` runs four gates in order, cheapest first, and returns nothing (with no state change)
if any of them declines:

1. **basis** (`basis.rs`): is this a real, tradeable spread? The observation must be fresh, backed
   by real pool liquidity, and past the entry threshold. A wide basis on a thin or stale pool is a
   pricing artifact, not a spread you can close into, so it is rejected.
2. **sizing** (`sizing.rs`): continuous-return Kelly, `f* = mu / sigma^2`, off the expected
   holding-period return and its volatility (not a win probability, because this is not a binary
   bet). The fractional-Kelly result is capped per position, then cut by a correlation penalty and
   a drawdown circuit breaker. The penalties apply after the cap on purpose: a drawdown must
   shrink the position even when Kelly wanted more.
3. **risk** (`risk.rs`): per-entry, bankroll-fraction, and gross-exposure limits, plus a daily and
   weekly drawdown kill switch. In-memory here; the on-chain `MandateGuard` enforces the same
   spirit at the contract boundary.
4. **neutral** (`neutral.rs`): build the two matched legs. Share quantity is matched across legs
   (not notional), so a move in the underlying cancels and the captured edge is the basis on those
   shares. Perp rich means long spot and short perp; perp cheap means the reverse. The residual
   delta is zero by construction.

## Boundaries

- Volatility and expected-return estimates are the caller's input. The engine does not fit a vol
  model; it applies the sizing discipline to whatever estimate it is given.
- Funding carry is folded into `expected_return` upstream. Modeling the venue's own funding is a
  signal-layer concern, not the engine's.
- The engine decides; it does not execute. Turning a plan into signed transactions, and reconciling
  fills, is the job of the layer above.
