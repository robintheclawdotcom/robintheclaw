# Developer guide

## Prerequisites

- Node.js 20 or newer for `signal/`, `verifier/`, and `web/`.
- Rust stable for `engine/`.
- Rust stable for the private `runtime/` collector.
- Foundry with Solidity 0.8.28 for `contracts/`.
- Public Robinhood Chain RPC access. The checked-in RPC URLs require no API key.

Never place private keys, exchange credentials, or deployment tokens in tracked files. Local
credentials belong under ignored `keys/` files with restrictive file permissions.

## Repository checks

Run these from the repository root after a clean dependency install:

```sh
node config/validate.mjs
(cd contracts && forge fmt --check && forge test -vvv)
(cd engine && cargo fmt --check && cargo clippy -- -D warnings && cargo test)
(cd runtime && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test)
(cd verifier && npm test)
(cd web && npm ci && npm run build)
./scripts/check-no-leaks.sh
```

`config/validate.mjs` validates chain IDs, address shapes, the mainnet address cross-checks, and
the testnet readiness gate. It does not grant permission to deploy.

## Signal measurement

```sh
cd signal
node src/discover.mjs NVDA
node src/spot.mjs NVDA
node src/basis.mjs
```

`discover.mjs` recovers Uniswap v4 pool keys into ignored local data. `spot.mjs` compares the
deepest discovered pool with Lighter's mark. `basis.mjs` is a perp-only sanity view. Output is
measurement data, not an order recommendation. The scanner exits nonzero for an unknown symbol.

## Deterministic planning

```sh
cd engine
cargo run --bin plan -- fixtures/plan-input.json
```

The CLI prints a JSON decision. `approved` contains matched spot/perp legs; `declined` identifies
the basis, sizing, or risk stage. Inputs must be finite. The gross risk check includes both legs.
Do not treat the fixture's synthetic expected return or volatility as production calibration.

## High-frequency research runtime

`runtime/` is a private worker, not a trading bot. It records Lighter public WebSocket events and
Robinhood Chain blocks, gas prices, and PoolManager logs. Its raw evidence archive is Cloudflare
R2; its normalized state is Render Postgres. See [research runtime](research-runtime.md) for the
schema, source behavior, and managed environment requirements.

```sh
cd runtime
cargo test
```

The process refuses to start without a database and R2 configuration. It does not accept a wallet,
private key, or venue write credential.

## Contract deployment modes

`Deploy.s.sol` is production-oriented and requires explicit `OWNER`, `AGENT`, and `ASSET` values.
It defaults to chain 4663 and rejects an asset or router with no bytecode. It is intentionally
unable to select a testnet asset automatically.

`DeployTestnet.s.sol` is the proof-path deployment. It is fixed to chain 46630, deploys a named
`tUSDG` fixture, and configures no execution target. It validates role separation and establishes
only custody plus attestation plumbing.

## Public verification

```sh
cd verifier
npm run verify:testnet-proof
```

The command reads the tracked deployment record and synthetic fixture, verifies all deployed role
relationships, recomputes the root, and compares it with the chain. It needs only public RPC
access and has no signing capability.
