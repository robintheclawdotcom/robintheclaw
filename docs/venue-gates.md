# Venue integration

## Current foundation

Mainnet market data and configured contract addresses are ready for research and integration work.
The testnet contract stack is in place; the next phase is connecting it to a validated venue
adapter and order lifecycle.

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
7. An exact target/selector allowlist and a per-window cap sized in the asset's native decimals.

## Execution readiness

An integration is ready to connect when the following are complete:

- Independent review of adapter code and ABI/API payload construction.
- Engine inputs calibrated from frozen historical data, including costs.
- Partial-fill state machine and emergency unwind covered by tests.
- Oracle/sequencer health policy covered by tests.
- Onchain cap, operator approval gate, and venue-specific limits reviewed together.
- Testnet end-to-end evidence published and independently verified.

This work keeps the execution layer aligned with the strategy engine, venue semantics, and
operational controls from the first live integration onward.

## Research and shadow gates

The runtime may capture public data continuously. It may not turn that data into a live order.
Before a strategy enters paired shadow execution, it must have a registered hypothesis, immutable
dataset snapshot, verified executable spot and perp quote sources, and a cost model that includes
fees, gas, funding, impact, and quote age.

Where a strategy uses convergence or regime assumptions, it must also demonstrate rolling
cointegration, residual stationarity, capacity, and an HMM `unknown`/`dislocated` veto before
it can create a shadow intent. Portfolio sizing requires shrinkage covariance, concentration, and
factor-exposure controls rather than independent per-trade Kelly allocations.

Before any mainnet deployment is considered, retain at least 180 calendar days of capture and 60
continuous shadow days covering off-hours, weekends, volatile windows, and low-liquidity windows.
Require embargoed walk-forward results, adjusted significance of at least 3.0, positive
lower-confidence net return, bounded capacity, partial-fill and unwind tests, independent contract
audit, and operational key review. These are additive to the execution enablement checklist.
