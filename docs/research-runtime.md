# Research runtime

## Purpose

`runtime/` is the private, continuous data-capture and shadow-execution runtime. It determines
whether registered hypotheses retain positive net economics across market regimes after realistic
costs and execution assumptions. It collects point-in-time market evidence and evaluates those
hypotheses. It cannot sign orders, hold funds, call a venue write API, or connect to a wallet.

The public website is not part of this runtime. It receives no research database connection,
object-storage credential, venue credential, or strategy configuration.

## Capture model

The collector connects to Lighter's public WebSocket and records configured active perp markets.
It subscribes to order-book, ticker, trade, market-stats, and height channels. Market stats carry
funding and open interest. Order-book nonces and publisher timestamps are retained; a nonce gap
ends the session so the collector can resubscribe from a fresh snapshot rather than joining
incompatible deltas. It sends the required WebSocket keepalive frame every minute.

The chain collector polls the configured Robinhood Chain RPC for new blocks, gas prices, and
PoolManager logs. It records block identity and marks data as confirmed, not finalized. The
configured sequencer feed is retained as a source reference but is not parsed until its published
message schema is independently verified.

Every raw event has a source and runtime version, publisher and receive times when available,
sequence and block identity where applicable, a SHA-256 wire-payload hash, and a private R2 object
key plus a normalized Postgres record. R2 stores a zstd-compressed copy of the original wire payload;
the digest is calculated before compression.

R2 is the raw evidence store. Postgres contains normalized events, feature snapshots, registered
strategies, immutable dataset manifests, shadow intents, legs, reconciliation state, and risk
snapshots. An object that reaches R2 before a database transaction may be orphaned after a database
failure; it is never treated as an accepted event until its Postgres row exists.

## Paper execution

The production paper agent consumes durable Lighter ticker events and requests a block-pinned,
size-specific Uniswap v4 exact-input quote for every configured market evaluation. It records the
quote, Quoter gas estimate, block identity, token decimals, Stock Token multiplier state, perp bid,
fees, and modeled costs before calculating net edge.

Repeated event delivery cannot inflate activity: the database evaluates each source event once for
a strategy version and permits one active opportunity episode per strategy and market. New ticks
update or close that episode instead of creating independent trades. Missing or stale evidence is a
decline, never a synthetic fill.

The initial strategy supports long spot and short perpetual exposure only. The complete production
runbook is in [Mainnet paper trading](paper-trading-operations.md).

## Environment

| Variable | Purpose |
| --- | --- |
| `DATABASE_URL` | Render Postgres private connection string. |
| `DATABASE_MIGRATIONS_URL` | Direct private connection used only to apply migrations. |
| `R2_BUCKET` | Private raw-event bucket name. |
| `AWS_ENDPOINT_URL` | Account-scoped Cloudflare R2 S3 endpoint. |
| `AWS_ACCESS_KEY_ID` | R2 token access key. |
| `AWS_SECRET_ACCESS_KEY` | R2 token secret. |
| `AWS_SESSION_TOKEN` | R2 session token when temporary credentials are used. |
| `AWS_REGION` | `auto` for R2. |
| `RUNTIME_CONFIG` | Checked-in, non-secret address configuration. |
| `PAPER_AGENT_CONFIG` | Checked-in paper strategy registration. |
| `ROBINHOOD_RPC_URL` | Authenticated Robinhood Chain mainnet RPC. |
| `RUST_LOG` | Runtime logging filter. |

No environment variable enables live trading. There is no execution mode setting and no signing
key path in the runtime.

## Research gates

A strategy can move from `registered` to `evaluating`, `shadow`, `rejected`, or `retired`. A
registration records its hypothesis, parameters, and dataset snapshot before evaluation. Promotion
requires frozen data, embargoed walk-forward tests, realistic net costs, bounded capacity, and
reproducible results.

Long capture and continuous shadow windows remain useful evidence for durability and cohort
expansion. They are not elapsed-time gates for the fixed capped mainnet canary. Canary admission is
based on the internally audited release and current technical account readiness; ongoing evidence
governs whether the strategy expands, contracts, or retires.
