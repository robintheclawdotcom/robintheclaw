# Venue integration

## Current foundation

The typed mainnet contract layer is deployed and source-verified on chain ID 4663. Market and route
configuration now advances through the staged integration process described below. The separate
testnet application stack remains available for product workflows.

`config/addresses.json` contains two deployment sections:

- `mainnet`: verified USDG and Universal Router references for chain 4663.
- `testnet`: `asset` and `universalRouter` are null with status `blocked` for chain 46630.

The production deployment script requires explicit environment values and validates bytecode. The
testnet proof script deploys an isolated fixture instead of borrowing a mainnet address.

## Building a venue adapter

For each spot or perp venue, the integration work records the following in a reviewed change:

1. Chain ID, contract/API endpoint, and source of truth.
2. Code hash or API version, supported symbols, collateral asset, and decimal handling.
3. Authentication and signing scheme, nonce model, idempotency behavior, and rate limits.
4. Order lifecycle semantics: simulation, submission, cancel, fill, partial fill, reject, and
   reconciliation.
5. Slippage, price bands, leverage, margin, liquidation, funding, and fee treatment.
6. Testnet evidence for both a successful order and safe unwind.
7. A typed intent, internally constructed route, and per-window cap sized in the asset's native
   decimals.

## Execution readiness

An integration is ready to connect when the following are complete:

- Internal review of adapter code and ABI/API payload construction.
- Engine inputs calibrated from frozen historical data, including costs.
- Partial-fill state machine and emergency unwind covered by tests.
- Oracle/sequencer health policy covered by tests.
- Onchain cap, operator approval gate, and venue-specific limits reviewed together.
- End-to-end evidence retained with reproducible transaction and reconciliation records.

This work keeps the execution layer aligned with the strategy engine, venue semantics, and
operational controls from the first live integration onward.

## Strategy evidence

The runtime captures public data continuously. The capped canary may turn a fresh approved
evaluation into a live order only when its immutable strategy manifest, executable spot and perp
quotes, account binding, and cost model all match the production policy.

Where a strategy uses convergence or regime assumptions, it must also demonstrate rolling
cointegration, residual stationarity, capacity, and an HMM `unknown`/`dislocated` veto before
it can create a shadow intent. Portfolio sizing requires shrinkage covariance, concentration, and
factor-exposure controls rather than independent per-trade Kelly allocations.

Research history informs strategy promotion but is not an elapsed-time activation gate. Every live
entry still requires fresh authenticated state, a profitable executable quote after costs, bounded
capacity, and a complete reduce-only exit path. The initial policy permits one account, one active
episode, $25 per leg, $50 gross, and $50 daily turnover.
