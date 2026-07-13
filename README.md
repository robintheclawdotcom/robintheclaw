# Robin the Claw

A consumer-grade, autonomous delta-neutral trading system for tokenized markets on Robinhood Chain. Robin the Claw finds
cross-venue basis between tokenized-equity spot liquidity and matching perpetuals, then turns
that market structure into coordinated trading plans. The stack combines venue-native data,
adaptive models, matched execution, continuous learning, and a no-code strategy application.

## Why

Tokenized markets are becoming a serious venue for global, programmable financial products.
Robin the Claw gives those markets an intelligent, disciplined execution layer
that can recognize relative-value opportunities and act on them with precision.

- **Market intelligence:** discovers tokenized-equity pools, compares them with perpetual markets,
  and builds the event history needed to understand cross-venue market structure.
- **Trade planning:** combines liquidity, fractional-Kelly sizing, portfolio awareness, and matched
  spot/perp legs into deterministic strategy plans.
- **Research models:** develops convergence, regime, and hedge-ratio models from the growing
  proprietary event store, then promotes the strongest ideas into the strategy stack.
- **Execution foundation:** runs a source-verified typed vault, risk manager, spot adapter, Safe,
  and timelock on Robinhood Chain mainnet, ready for staged market activation.
- **Personal strategy accounts:** email, passkey, social, and wallet login lead to a stable smart
  account, a personal vault, a unified dashboard, and one-operation testnet onboarding.
- **Record integrity:** commits strategy records onchain as a supporting tool for research,
  operations, and inspection.

## Layout

```
config/      canonical chain + venue addresses and deployment readiness gates
app/         authenticated Rust product API, Postgres persistence, and activity indexing
contracts/   Foundry workspace for shared and personal strategy vaults
engine/      deterministic basis, sizing, risk, and neutral-plan engine
runtime/     continuous market capture and research runtime
signal/      read-only basis scanner (measurement before execution)
sdk/         TypeScript client (later)
verifier/    record-integrity tool
web/         public site plus authenticated Robin application
docs/        system design and integration notes
```

## Building the stack

Robin already has working foundations across the stack:

- `signal/` discovers pools and measures live basis across the tokenized-equity universe.
- `engine/` transforms observations into delta-neutral spot/perp plans with adaptive sizing.
- `runtime/` continuously captures venue and chain events into the research dataset.
- `contracts/` establish the onchain custody, strategy-role, and execution-policy foundation.
- `app/` authenticates Privy sessions, persists product state, verifies vault receipts, and builds
  real dashboard responses.
- `verifier/` provides an inspectable record pipeline alongside the strategy stack.
- `web/` provides the public narrative, one-click onboarding, strategy controls, activity,
  settings, and multi-wallet portfolio management.

```bash
# read the live basis across the universe
cd signal && node src/basis.mjs

# contract tests
cd contracts && forge test -vv
```

Robin's typed production contracts are deployed and source-verified on Robinhood Chain mainnet.
The system launched halted and unfunded with no installed agent or configured market, establishing
the production governance and custody boundary for staged activation. The personal-vault factory,
test asset, and faucet remain live on Robinhood Chain testnet, while the paid application service,
private API, and dedicated product database run on Render. The initial research universe is the 21
Stock Tokens that also have a live perp.

## Website and application

The public site and authenticated product live in `web/`. Authenticated requests pass through
same-origin Next.js handlers to the private Rust API; provider credentials and the application
database remain in managed service settings.

The default product path is no-code: sign in, create or restore a strategy account, link funding
wallets, create a personal vault, and manage it from the dashboard. A separately managed
WalletConnect project is not a launch dependency; Privy provides embedded login and named external
wallet connections.

```bash
cd web
npm ci
npm run build
```

## Developer documentation

The full specification lives in [docs/index.md](docs/index.md). The website copies that source
set during its build so the public documentation and repository documentation describe the same
system boundaries. The [user experience specification](docs/user-experience.md) defines the
account model, onboarding state machine, dashboard, and multi-wallet behavior. The
[edge research methodology](docs/research-methodology.md) defines the
model hierarchy, RWA-specific data requirements, and evidence needed to promote a strategy.
The [mainnet deployment record](docs/mainnet-deployment.md) documents the live contract graph,
governance, runtime code hashes, review evidence, and staged-activation controls.

## Direction

Robin is designed to grow from market intelligence into a full trading platform for tokenized
markets. The roadmap extends the current stack with cointegration and Ornstein-Uhlenbeck spread
models, adaptive Kalman hedge ratios, hidden-Markov regime classification, portfolio covariance,
and execution-aware venue routing. The planned large-model research loop will turn event data and
market context into better hypotheses, tests, and post-trade analysis.

Tokenized stocks on Robinhood Chain are geo-restricted and are not available to U.S. persons.

## License

Apache-2.0.
