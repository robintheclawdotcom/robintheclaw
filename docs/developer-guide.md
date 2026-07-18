# Developer guide

## Prerequisites

- Node.js 24 or newer for `web/`; Node.js 20 or newer for `signal/` and `verifier/`.
- Rust stable for `engine/` and `app/`.
- Rust stable for the private `runtime/` collector.
- Foundry with Solidity 0.8.28 for `contracts/`.
- Provider RPCs for Robinhood Chain mainnet contract verification and testnet application work.

Never place private keys, exchange credentials, or deployment tokens in tracked files. Local
credentials belong under ignored `keys/` files with restrictive file permissions.

## Repository checks

Run these from the repository root after a clean dependency install:

```sh
node config/validate.mjs
(cd contracts && forge fmt --check && forge test -vvv)
(cd engine && cargo fmt --check && cargo clippy -- -D warnings && cargo test)
(cd app && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test)
(cd runtime && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test)
(cd verifier && npm test)
(cd web && npm ci && npm test && npm run typecheck && npm run build)
./scripts/check-no-leaks.sh
```

`config/validate.mjs` validates chain IDs, address shapes, the canonical mainnet deployment
references, and the testnet readiness gate. It does not activate a market or authorize capital.

## Market intelligence

```sh
cd signal
node src/discover.mjs NVDA
node src/spot.mjs NVDA
node src/basis.mjs
```

`discover.mjs` recovers Uniswap v4 pool keys into ignored local data. `spot.mjs` compares the
deepest discovered pool with Lighter's mark. `basis.mjs` is a perp-only sanity view. Together they
form the input layer for strategy research and planning. The scanner exits nonzero for an unknown
symbol.

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

The planned model hierarchy, RWA-specific source requirements, and promotion criteria are defined
in [edge research methodology](research-methodology.md). Do not add a model to the execution path
without implementing its documented validation and fail-closed behavior.

## Contract deployment modes

`DeployGovernance.s.sol` verifies a canonical Safe proxy, SafeL2 singleton, Safe version, owner set,
threshold, and code hashes before creating a self-administered OpenZeppelin timelock.

`Deploy.s.sol` is fixed to Robinhood Chain mainnet, chain ID 4663. It verifies the Safe and
timelock roles, USDG decimals, canonical Universal Router and Permit2 addresses and code hashes,
explicit risk limits, and distinct governance roles before deploying the typed v1 graph. The
factory always creates the vault with a zero agent, halted risk mode, zero balances, no market,
no route, and an unbound fail-closed sequencer gate.

`VerifyDeployment.s.sol` is the mandatory read-only post-deployment check. It validates
factory-child provenance, all internal references and governance roles, runtime code hashes,
external contract hashes, exact limits, zero balances, empty market state, zero agent, halted mode,
and the unbound gate. The canonical release record is
[`deployments/mainnet.json`](../deployments/mainnet.json); do not update it until the verifier and
source verification both pass.

The live addresses and exact environment surface are documented in
[mainnet contract deployment](mainnet-deployment.md) and [`contracts/README.md`](../contracts/README.md).

`DeployTestnet.s.sol` is the proof-path deployment. It is fixed to chain 46630, deploys a named
`tUSDG` fixture, and configures no execution target. It validates role separation and establishes
only custody plus attestation plumbing.

`DeployUxTestnet.s.sol` is the application deployment. It is fixed to chain 46630 and deploys the
test asset, one-claim faucet, and versioned personal-vault factory. Set `DEPLOYER`, `AGENT`,
`WINDOW_CAP`, and `WINDOW_SECONDS`, then record the confirmed addresses only in managed service
settings.

## Product application

The authenticated Rust routes require `DATABASE_URL`, `PRIVY_APP_ID`, `PRIVY_APP_SECRET`,
`PRIVY_VERIFICATION_KEY`, provider `APP_RPC_URL`, and the three confirmed application contract
addresses. The web service requires the public Privy app ID, the private API host, the Privy
verification key, and a server-only Alchemy API key for the legacy testnet wallet proxy. Render
owns these values; do not place them in tracked environment files.

The API runs migrations at startup. Next.js validates a Privy access token before creating the
HTTP-only same-origin session cookie. The Rust API validates the token again and resolves wallet
ownership from Privy server-side. The server never trusts client-supplied wallet lists or signs an
owner operation.

Alchemy Wallet API traffic uses `POST /api/wallet`. This authenticated proxy accepts only the
prepare, submit, and status methods used by the application, verifies each prepared batch against
the user's server-side account and vault state, removes client-supplied paymaster data, rate-limits
the session, and forwards the request without exposing provider credentials to the browser. The
proxy always strips paymaster capabilities. The signing account must hold enough ETH on Robinhood
Chain to pay gas. Mainnet owner actions use the connected owner wallet directly and do not depend
on this proxy or an Alchemy gas policy.

## Record integrity

```sh
cd verifier
npm run verify:testnet-proof
```

The command reads the tracked deployment record and synthetic fixture, checks the deployed role
relationships, recomputes the root, and compares it with the chain. It gives developers a direct
view of the current onchain foundation.
