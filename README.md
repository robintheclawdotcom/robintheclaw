# Robin the Claw

Autonomous, delta-neutral trading infrastructure for tokenized markets on Robinhood Chain. Robin
the Claw identifies cross-venue basis between tokenized-equity spot liquidity and corresponding
perpetual markets, then converts validated edge into portfolio-aware, matched execution.

The system spans venue-native data, proprietary research, deterministic planning, onchain custody,
and operator control.

## Core system

- **Market data:** normalizes tokenized-equity spot liquidity, perpetual markets, funding, chain
  state, and source health into point-in-time datasets.
- **Research:** evaluates executable net edge, convergence, regime, hedge-ratio, capacity, and
  model decay across frozen datasets and auditable shadow runs.
- **Portfolio construction:** applies fractional Kelly, concentration limits, exposure controls,
  and matched spot/perpetual sizing through a deterministic engine.
- **Execution controls:** source-verified contracts enforce custody, mandate, routing, limits, and
  governance on Robinhood Chain mainnet.
- **Strategy operations:** the application consolidates capital, exposure, positions, activity,
  wallets, and mandate controls.
- **Record integrity:** deterministic onchain commitments support attribution, reconciliation, and
  independent analysis.

## Layout

```
config/      canonical network, contract, and venue configuration
app/         authenticated Rust product API, Postgres persistence, and activity indexing
contracts/   Foundry workspace for shared and personal strategy vaults
engine/      deterministic basis, sizing, risk, and neutral-plan engine
runtime/     continuous market capture and research runtime
signal/      market discovery and cross-venue basis scanner
verifier/    record-integrity tool
web/         public site plus authenticated Robin application
docs/        system design and integration notes
```

## System status

The repository contains production and research components across the full operating path:

- `signal/` discovers pools and measures live basis across the tokenized-equity universe.
- `engine/` transforms observations into delta-neutral spot/perpetual plans with adaptive sizing.
- `runtime/` continuously captures venue and chain events into the research dataset.
- `contracts/` establish the onchain custody, strategy-role, and execution-policy foundation.
- `app/` authenticates Privy sessions, persists product state, verifies vault receipts, and builds
  real dashboard responses.
- `verifier/` provides an inspectable record pipeline alongside the strategy stack.
- `web/` provides public documentation and the authenticated strategy-operations interface.

```bash
# read the live basis across the universe
cd signal && node src/basis.mjs

# contract tests
cd contracts && forge test -vv
```

Robin's typed production contracts are deployed and source-verified on Robinhood Chain mainnet.
The deployment establishes production custody, governance, mandate enforcement, and spot-routing
controls for autonomous strategy execution. Personal-vault infrastructure is active on Robinhood Chain
testnet. The application, private API, and dedicated database run on Render. The initial research
universe covers the 21 Stock Tokens with a corresponding perpetual market.

## Website and application

The public site and authenticated application live in `web/`. Authenticated requests pass through
same-origin Next.js handlers to the private Rust API; provider credentials and the application
database remain in managed service settings.

The operator interface covers portfolio state, strategy controls, positions, activity, wallet
connections, recovery, and account settings. Privy provides embedded authentication and external
wallet connections; Alchemy provides smart-account and sponsored-transaction infrastructure.

```bash
cd web
npm ci
npm run build
```

## Developer documentation

The canonical specification lives in [docs/index.md](docs/index.md) and is published by the website
build. The [user experience specification](docs/user-experience.md) defines the account model,
onboarding state machine, dashboard, and multi-wallet behavior. The
[research methodology](docs/research-methodology.md) defines the model hierarchy, RWA-specific
data requirements, and promotion evidence. The
[mainnet deployment record](docs/mainnet-deployment.md) documents the live contract graph,
governance, runtime code hashes, and activation controls.

## Research direction

Robin is designed to compound research and execution advantage as tokenized markets mature. The
roadmap adds rolling cointegration, Ornstein-Uhlenbeck residual models, Kalman hedge ratios,
hidden-Markov regime controls, shrinkage covariance, and execution-aware routing. A private
large-model research loop supports hypothesis generation, issuer-document parsing, and post-trade
analysis.

Tokenized stocks on Robinhood Chain are geo-restricted and are not available to U.S. persons.

## License

Apache-2.0.
