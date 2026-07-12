# Venue and execution gates

## Current decision

Mainnet market data and configured contract addresses are validated as production references.
Testnet execution is blocked. This is a deliberate configuration state, not a missing default.

`config/addresses.json` contains two deployment sections:

- `mainnet`: verified USDG and Universal Router references for chain 4663.
- `testnet`: `asset` and `universalRouter` are null with status `blocked` for chain 46630.

The production deployment script requires explicit environment values and validates bytecode. The
testnet proof script deploys an isolated fixture instead of borrowing a mainnet address.

## Evidence required to unblock a venue

For each spot or perp venue, record the following in a reviewed change before it may be allowlisted:

1. Chain ID, contract/API endpoint, and source of truth.
2. Code hash or API version, supported symbols, collateral asset, and decimal handling.
3. Authentication and signing scheme, nonce model, idempotency behavior, and rate limits.
4. Order lifecycle semantics: simulation, submission, cancel, fill, partial fill, reject, and
   reconciliation.
5. Slippage, price bands, leverage, margin, liquidation, funding, and fee treatment.
6. Testnet evidence for both a successful order and safe unwind.
7. An exact target/selector allowlist and a per-window cap sized in the asset's native decimals.

## Execution enablement checklist

An owner may consider adding a target only after all of the following are complete:

- Independent review of adapter code and ABI/API payload construction.
- Engine inputs calibrated from frozen historical data, including costs.
- Partial-fill state machine and emergency unwind covered by tests.
- Oracle/sequencer health policy covered by tests.
- On-chain cap, operator approval gate, and venue-specific limits reviewed together.
- Testnet end-to-end evidence published and independently verified.

The absence of any item keeps the venue blocked. There is no override based on a promising basis
observation.
