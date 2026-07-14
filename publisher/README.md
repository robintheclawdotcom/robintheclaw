# Account state publisher

This service reconstructs account-scoped Lighter and Robinhood Chain state and publishes short-lived, HMAC-authenticated snapshots to the execution coordinator and product readiness API. It starts disabled and has no order, transfer, withdrawal, or activation endpoint.

Every cycle begins with authoritative account discovery. The publisher reads active registered accounts from the coordinator, exact active graphs from the Robinhood provisioner, and deployment and execution transaction hashes from the Robinhood signer journal. It uses database roles with `SELECT` only and also sets each connection to read-only. A missing or mismatched tenant graph omits that tenant and keeps the service unready; a cross-tenant collision or malformed shared journal aborts the cycle. Accounts that become blocked or closed are absent from the next cycle and receive no new snapshots.

Lighter credentials never enter this process. The publisher sends only `executionAccountId` to the private Lighter provisioner bridge. The provisioner resolves and verifies the account-bound credential, reconstructs account, active-order, trade, and next-nonce state, and returns public evidence over a separately keyed, replay-protected HMAC channel. The publisher verifies the response signature and exact execution-account, Lighter-account, API-key, market, and credential-version identity.

The Robinhood adapter requires two independent RPC provider hostnames. At the same finalized block it compares chain ID, sync status, safe and finalized heads, canonical registry and vault bindings, vault code hash, owner and execution gas, USDG balance, risk state, and every authoritative receipt. A difference, reorg, missing receipt, failed receipt, or rate limit fails closed.

## Policy activation boundary

`policy_active` is derived only from coordinator state for the exact registered execution account. Global and strategy controls must be `ACTIVE`; the account control may be `REDUCE_ONLY` while an otherwise ready account waits for launch, or `ACTIVE` after launch. The account and strategy manifest digests must equal the registered digest; exactly one current coordinator AAPL market configuration must match the publisher's pinned Lighter market ID; and venue approval, oracle, sequencer, reconciliation, exit authority, alerting, and safe rotation must all be ready. Missing rows, nulls, overlaps, mismatches, `HALTED`, or one false gate publish `false`. This readiness evidence never authorizes an entry: the coordinator separately requires global, strategy, and account controls all to be `ACTIVE` when it admits and sends an entry.

## Configuration

Set:

- `ACCOUNT_PUBLISHER_ENABLED=true`
- `ACCOUNT_PUBLISHER_COORDINATOR_DATABASE_URL` to a coordinator role with `SELECT` only
- `ACCOUNT_PUBLISHER_ROBINHOOD_DATABASE_URL` to a Robinhood provisioner role with `SELECT` only
- `ACCOUNT_PUBLISHER_ROBINHOOD_JOURNAL_DATABASE_URL` to a Robinhood signer-journal role with `SELECT` only
- `ACCOUNT_PUBLISHER_PRIMARY_RPC_URL` and `ACCOUNT_PUBLISHER_SECONDARY_RPC_URL` to independent Robinhood RPC providers
- `ACCOUNT_PUBLISHER_LIGHTER_BRIDGE_URL` plus shared `LIGHTER_PUBLISHER_BRIDGE_CALLER_ID` and `LIGHTER_PUBLISHER_BRIDGE_HMAC_KEY`
- `ACCOUNT_PUBLISHER_COORDINATOR_URL`, `ACCOUNT_PUBLISHER_COORDINATOR_CALLER_ID`, and `ACCOUNT_PUBLISHER_COORDINATOR_HMAC_KEY`
- `ACCOUNT_PUBLISHER_APPLICATION_URL`, `ACCOUNT_PUBLISHER_APPLICATION_CALLER_ID`, and `ACCOUNT_PUBLISHER_APPLICATION_HMAC_KEY`
- `ACCOUNT_PUBLISHER_LIGHTER_MARKET_ID` pinned to the approved AAPL perpetual
- `ACCOUNT_PUBLISHER_MINIMUM_COLLATERAL_RAW`, `ACCOUNT_PUBLISHER_MINIMUM_SETTLEMENT_RAW`, `ACCOUNT_PUBLISHER_MINIMUM_OWNER_GAS_RAW`, and `ACCOUNT_PUBLISHER_MINIMUM_SIGNER_GAS_RAW`
- optionally, `ACCOUNT_PUBLISHER_POLL_MILLISECONDS` from 4000 through 4500

The three HMAC environment secrets must be distinct 32-byte lowercase hex values. There is no mounted config file, account list, Lighter credential, expected-nonce file, or receipt-journal file in publisher configuration. Render can wire service URLs with `fromService` and keep the HMACs as synchronized secret environment variables.

The database roles require `SELECT` on:

- coordinator: `execution_account_registrations`, `execution_accounts`, `execution_control`, `execution_strategy_control`, `execution_account_control`, `execution_account_readiness`, and `execution_market_configs`;
- Robinhood provisioner: `robinhood_execution_bindings` excluding any need to expose `kms_key_id`;
- Robinhood signer: `robinhood_signer_deployments` and `robinhood_signer_transactions` excluding signed transaction bytes.

```bash
go test ./...
go vet ./...
docker build -t robin-account-publisher .
```
