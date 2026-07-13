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

## Shadow execution

The shadow model evaluates only on economically relevant events. It requires fresh executable
quotes for both legs, records bid/ask depth, applies fees, gas, and a depth-based impact estimate,
and produces `declined`, `proposed`, `partially_hedged`, `hedged`, `unhedged`, `cancelled`,
`expired`, `unwound`, or `stale`.

Repeated event delivery cannot inflate a trade count: the database enforces one dedupe key per
strategy, source event, and direction. The current collector persists perp features but does not
create paired intents until a separately verified Uniswap quoting adapter supplies executable spot
bid/ask and depth. Missing spot data is a decline, never a synthetic fill.

## Environment

| Variable | Purpose |
| --- | --- |
| `DATABASE_URL` | Render Postgres private connection string. |
| `R2_BUCKET` | Private raw-event bucket name. |
| `AWS_ENDPOINT_URL` | Account-scoped Cloudflare R2 S3 endpoint. |
| `AWS_ACCESS_KEY_ID` | R2 token access key. |
| `AWS_SECRET_ACCESS_KEY` | R2 token secret. |
| `AWS_REGION` | `auto` for R2. |
| `RUNTIME_CONFIG` | Checked-in, non-secret address configuration. |
| `RUST_LOG` | Runtime logging filter. |

No environment variable enables live trading. There is no execution mode setting and no signing
key path in the runtime.

## Research gates

A strategy can move from `registered` to `evaluating`, `shadow`, `rejected`, or `retired`. A
registration records its hypothesis, parameters, and dataset snapshot before evaluation. Promotion
requires frozen data, embargoed walk-forward tests, realistic net costs, bounded capacity, and
reproducible results.

Six months of capture and sixty continuous shadow days are the minimum evidence required to argue
for a durable edge. That evidence supports the staged promotion of the deployed mainnet contract
layer into controlled capital activation.
