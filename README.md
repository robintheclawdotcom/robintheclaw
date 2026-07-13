# Robin the Claw

A delta-neutral trading system for tokenized markets on Robinhood Chain. Robin the Claw finds
cross-venue basis between tokenized-equity spot liquidity and matching perpetuals, then turns
that market structure into coordinated trading plans. The ambition is a complete autonomous
stack: venue-native data, adaptive models, matched execution, and continuous learning.

## Why

Tokenized markets are becoming a serious venue for global, programmable financial products.
Robin the Claw is being built to give those markets an intelligent, disciplined execution layer
that can recognize relative-value opportunities and act on them with precision.

- **Market intelligence:** discovers tokenized-equity pools, compares them with perpetual markets,
  and builds the event history needed to understand cross-venue market structure.
- **Trade planning:** combines liquidity, fractional-Kelly sizing, portfolio awareness, and matched
  spot/perp legs into deterministic strategy plans.
- **Research models:** develops convergence, regime, and hedge-ratio models from the growing
  proprietary event store, then promotes the strongest ideas into the strategy stack.
- **Execution foundation:** pairs custody and delegated execution contracts with a clean path to
  venue adapters, position workflows, and operational control.
- **Record integrity:** commits strategy records onchain as a supporting tool for research,
  operations, and inspection.

## Layout

```
config/      canonical chain + venue addresses and deployment readiness gates
contracts/   Foundry workspace (MandateGuard, StrategyVault, AttestationAnchor)
engine/      deterministic basis, sizing, risk, and neutral-plan engine
runtime/     continuous market capture and research runtime
signal/      read-only basis scanner (measurement before execution)
sdk/         TypeScript client (later)
verifier/    record-integrity tool
zk/          zero-knowledge proof that net return cleared a threshold, trades private
web/         public Next.js site and developer interface
docs/        system design and integration notes
```

## Building the stack

Robin already has working foundations across the stack:

- `signal/` discovers pools and measures live basis across the tokenized-equity universe.
- `engine/` transforms observations into delta-neutral spot/perp plans with adaptive sizing.
- `runtime/` continuously captures venue and chain events into the research dataset.
- `contracts/` establish the onchain custody, strategy-role, and execution-policy foundation.
- `verifier/` provides an inspectable record pipeline alongside the strategy stack.
- `web/` publishes the architecture, developer documentation, and testnet progress.

```bash
# read the live basis across the universe
cd signal && node src/basis.mjs

# contract tests
cd contracts && forge test -vv
```

The first onchain foundation is deployed on Robinhood Chain testnet. Venue adapters and the
full execution lifecycle are the next major build-out. The initial universe is the 21 Stock
Tokens that also have a live perp.

## Public site

The public site lives in `web/`. It is a static Next.js export, so it can be deployed to a CDN
without exposing the execution runtime or its credentials.

```bash
cd web
npm ci
npm run build
```

## Developer documentation

The full specification lives in [docs/index.md](docs/index.md). The website copies that source
set during its build so the public documentation and repository documentation describe the same
system boundaries. The [edge research methodology](docs/research-methodology.md) defines the
model hierarchy, RWA-specific data requirements, and evidence needed to promote a strategy.

## Verifiable performance

An agent can prove its net return over a set of trades cleared a threshold without revealing any
individual trade. The `zk/` circuit produces a zero-knowledge proof checkable on-chain against the
agent identity, the claimed basis-point threshold, and a commitment binding the trades. This is how
a track record becomes something anyone can check rather than something you take on faith, while the
strategy that produced it stays private.

## Direction

Robin is designed to grow from market intelligence into a full trading platform for tokenized
markets. The roadmap extends the current stack with cointegration and Ornstein-Uhlenbeck spread
models, adaptive Kalman hedge ratios, hidden-Markov regime classification, portfolio covariance,
and execution-aware venue routing. Proof-of-PnL is the first verifiability primitive; binding a
proof to the complete anchored record, so a claim cannot cherry-pick winners, is the next step. The
planned large-model research loop will turn event data and market context into better hypotheses,
tests, and post-trade analysis.

Tokenized stocks on Robinhood Chain are geo-restricted and are not available to U.S. persons.

## License

Apache-2.0.
