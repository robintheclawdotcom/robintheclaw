# Robin the Claw

A systematic, delta-neutral RWA trading agent for Robinhood Chain. Robin the Claw targets
repeatable, risk-adjusted returns from tokenized-stock spot/perpetual basis and funding. It
matches exposures rather than taking a directional view, sizes within fractional-Kelly and
portfolio limits, and operates inside on-chain controls. Published trade records are committed in
a recomputable form so reported results can be independently reviewed.

## Approach

The system focuses on a narrow problem: identify basis and funding dislocations that remain
attractive after fees, funding, price impact, latency, and hedging risk. A displayed spread is
not a trade. Each candidate must clear executable-price, liquidity, sizing, and validation gates
before it can progress.

- **Research-led:** candidates require registered hypotheses, out-of-sample testing, and shadow
  evidence before any live-capital review.
- **Bounded:** the agent may operate only through a mandate enforced on-chain (`MandateGuard`):
  allowlisted venues and selectors, a per-window notional cap, and a kill switch. A call outside
  the mandate reverts.
- **Verifiable:** every batch of disclosed trades is Merkle-rooted and anchored on-chain
  (`AttestationAnchor`) in append-only sequence.
- **Risk-managed:** market-neutral construction, fractional-Kelly sizing, concentration limits,
  and drawdown controls prioritize capital preservation.

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

## Current stage

Research and testnet foundation. The components that run today are deliberately separated from
live capital:

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

## Scope and risk

- No live performance or durable edge is claimed. Basis opportunities can decay as capital enters,
  and every candidate must earn promotion through out-of-sample and shadow evidence.
- Market-neutral is not risk-free: basis can widen before it converges, legs can fill unevenly,
  and funding can invert. Position sizing and the mandate cap exist to bound those, not remove them.
- Tokenized stocks on Robinhood Chain are geo-restricted and are not available to U.S. persons.
  This software is infrastructure, not investment advice.

## License

Apache-2.0.
