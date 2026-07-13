# Robin the Claw

A delta-neutral trading system for tokenized markets on Robinhood Chain. Robin the Claw finds
cross-venue basis between tokenized-equity spot liquidity and matching perpetuals, then turns
that market structure into coordinated trading plans. The ambition is a complete autonomous
stack: market intelligence, adaptive sizing, matched execution, and continuous learning.

## Why

Tokenized markets are becoming a serious venue for global, programmable financial products.
Robin the Claw is being built to give those markets an intelligent, disciplined execution layer
that can recognize relative-value opportunities and act on them with precision.

- **Market intelligence:** discovers tokenized-equity pools, compares them with perpetual markets,
  and identifies the basis that can support a relative-value strategy.
- **Trade planning:** combines liquidity, fractional-Kelly sizing, exposure awareness, and matched
  spot/perp legs into deterministic strategy plans.
- **Execution foundation:** pairs custody and delegated execution contracts with a clean path to
  venue adapters, position workflows, and operational control.
- **Record integrity:** commits strategy records on chain as a supporting tool for research,
  operations, and inspection.

## Layout

```
config/      canonical chain + venue addresses and deployment readiness gates
contracts/   Foundry workspace (MandateGuard, StrategyVault, AttestationAnchor)
engine/      deterministic basis, sizing, risk, and neutral-plan engine
runtime/     high-frequency market capture and research runtime
signal/      read-only basis scanner (measurement before execution)
sdk/         TypeScript client (later)
verifier/    record-integrity tool
web/         public Next.js site and developer interface
docs/        system design and integration notes
```

## Building the stack

Robin already has working foundations across the stack:

- `signal/` discovers pools and measures live basis across the tokenized-equity universe.
- `engine/` transforms observations into delta-neutral spot/perp plans with adaptive sizing.
- `runtime/` captures high-frequency market data and accelerates strategy research.
- `contracts/` establish the on-chain custody, strategy-role, and execution-policy foundation.
- `verifier/` provides an inspectable record pipeline alongside the strategy stack.
- `web/` publishes the architecture, developer documentation, and testnet progress.

```bash
# read the live basis across the universe
cd signal && node src/basis.mjs

# contract tests
cd contracts && forge test -vv
```

The first on-chain foundation is deployed on Robinhood Chain testnet. Venue adapters and the
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

## Direction

Robin is designed to grow from market intelligence into a full trading platform for tokenized
markets: better data, smarter portfolio construction, venue adapters, resilient execution, and
an operating history that makes each iteration more informed than the last.

Tokenized stocks on Robinhood Chain are geo-restricted and are not available to U.S. persons.

## License

Apache-2.0.
