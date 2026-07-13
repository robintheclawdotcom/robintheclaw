# Robin the Claw

A market-neutral trading agent on Robinhood Chain. It captures the spread between a tokenized
stock's spot price and its perpetual (basis and funding), taking no directional view on the
stock, sized with quarter-Kelly discipline and bounded by on-chain limits it cannot exceed.
Every trade is committed to an on-chain, recomputable record, so the agent's track record is
verifiable rather than a screenshot you have to trust.

## Why

Agentic trading reached a wide audience through connect-your-AI products, but the record stays
custodial and opaque: you trust the agent's word for what it did. Robin the Claw inverts that.
The strategy is market-neutral and disciplined; the proof is public.

- **Bounded:** the agent trades only through a mandate enforced on-chain (`MandateGuard`):
  allowlisted venues and selectors, a per-window notional cap, and a kill switch. A call outside
  the mandate reverts.
- **Provable:** every batch of trades is Merkle-rooted and anchored on-chain (`AttestationAnchor`),
  append-only. Anyone recomputes the record from chain state.
- **Disciplined:** market-neutral, quarter-Kelly sizing, strategy selected on out-of-sample
  performance. Reasoning stays out of the execution hot path.

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
system boundaries.

## Honest scope

- This is not a returns product. The edge in a market-neutral basis trade is thin and temporary,
  and it decays as capital arrives. The durable part is the verifiable record and the discipline.
- Market-neutral is not risk-free: basis can widen before it converges, legs can fill unevenly,
  and funding can invert. Position sizing and the mandate cap exist to bound those, not remove them.
- Tokenized stocks on Robinhood Chain are geo-restricted and are not available to U.S. persons.
  This software is infrastructure, not investment advice.

## License

Apache-2.0.
