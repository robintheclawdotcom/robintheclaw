# Robin the Claw

A market-neutral trading agent on Robinhood Chain, built to find durable, risk-adjusted net
profitability in tokenized-stock basis and funding. It matches spot and perpetual exposures,
takes no directional view on the underlying, sizes with quarter-Kelly discipline, and operates
within on-chain limits it cannot exceed. Every trade is committed to an on-chain, recomputable
record so measured performance can be independently inspected.

## Why

Most agentic trading products automate execution without establishing that the strategy has a
durable net edge. Robin the Claw is built to do both: find profitable basis opportunities after
fees, funding, impact, and risk, then preserve enough evidence to measure whether that edge holds.

- **Edge-driven:** the agent pursues positive, risk-adjusted net returns from matched spot/perp
  basis and funding, selected only after out-of-sample and shadow evidence.
- **Bounded:** the agent trades only through a mandate enforced on-chain (`MandateGuard`):
  allowlisted venues and selectors, a per-window notional cap, and a kill switch. A call outside
  the mandate reverts.
- **Measured:** every batch of trades is Merkle-rooted and anchored on-chain (`AttestationAnchor`),
  append-only. The record makes realized performance auditable rather than anecdotal.
- **Disciplined:** market-neutral, quarter-Kelly sizing, concentration limits, and drawdown
  controls keep the pursuit of returns bounded by capital preservation.

## Layout

```
config/      canonical chain + venue addresses and deployment readiness gates
contracts/   Foundry workspace (MandateGuard, StrategyVault, AttestationAnchor)
engine/      deterministic basis, sizing, risk, and neutral-plan engine
runtime/     private high-frequency capture and shadow-execution runtime
signal/      read-only basis scanner (measurement before execution)
sdk/         TypeScript client (later)
verifier/    recompute-the-record tool
web/         public Next.js site and verifier interface
docs/        design + verification notes
```

## Status

Early. What runs today:

- `contracts/` compiles and tests green (`forge test`): the mandate, custody vault, and
  attestation anchor enforce access control, cap/window, append-only, and agent-to-anchor paths.
- `signal/` reads the live perp book for the tradable universe and reports each name's basis
  (perp mark vs index) in bps, appending to a JSONL series for later analysis.
- `runtime/` captures public Lighter and Robinhood Chain evidence into private managed stores. It
  has no signing capability and does not create live orders.

```bash
# read the live basis across the universe
cd signal && node src/basis.mjs

# contract tests
cd contracts && forge test -vv
```

Nothing here moves money yet, and no contract is deployed. The testnet deployment gate is
intentionally blocked until a canonical asset and execution venue are verified. The tradable
universe is the 21 Stock Tokens that also have a live perp.

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

## Honest scope

- The objective is positive, risk-adjusted net returns. That is a research and execution target,
  not a guarantee: basis edges can decay as capital arrives and every candidate must earn promotion
  with out-of-sample and shadow evidence.
- Market-neutral is not risk-free: basis can widen before it converges, legs can fill unevenly,
  and funding can invert. Position sizing and the mandate cap exist to bound those, not remove them.
- Tokenized stocks on Robinhood Chain are geo-restricted and are not available to U.S. persons.
  This software is infrastructure, not investment advice.

## License

Apache-2.0.
