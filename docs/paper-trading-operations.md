# Mainnet paper trading

## Operating model

Robin's paper agent evaluates the production basis strategy against live Robinhood Chain and
Lighter market data without sending orders. The collector and paper agent are separate Render
background workers connected to the private research database:

- `robin-research-collector` captures venue and chain events, maintains source health, and archives
  the original wire payloads.
- `robin-paper-agent` consumes durable ticker events, requests block-pinned Uniswap v4 quotes, and
  persists every evaluation and opportunity episode. It also assigns committed evaluations to
  each running user agent in the private product database.

Neither worker accepts inbound traffic. Neither process contains an EVM signer, Lighter API key,
wallet path, transaction broadcaster, or contract write interface.

## Strategy configuration

`runtime/config/mainnet-paper.json` is the versioned strategy registration. The first production
market is AAPL/USDG spot against the AAPL perpetual. Its reviewed route uses a zero-hook Uniswap v4
pool and a fixed $25 USDG exact-input amount. A qualifying evaluation opens a matched simulated
position: the quoted Stock Token output defines the spot quantity, and `uiMultiplier` defines the
corresponding underlying quantity sold on Lighter.

Each evaluation freezes:

- the source ticker event and receive time;
- the Robinhood block number and hash;
- the exact-input quote and Quoter gas estimate;
- token decimals and `uiMultiplier` state;
- oracle pause and multiplier-transition state;
- Lighter bid, modeled fees, and configured execution costs;
- gross and net edge in integer parts per million.

V1 evaluates only long spot and short perpetual exposure. A stale ticker, failed quote, paused
oracle, pending multiplier change, configuration mismatch, or sub-threshold net edge produces a
persisted decline.

## Opportunity episodes

The database permits one active paper position per strategy version and market. A qualifying tick
opens it; subsequent qualifying ticks mark both legs against a block-pinned reverse spot quote and
the Lighter ask. When the edge no longer qualifies, the position closes and records realized net
P&L after the spot round trip, perpetual entry and exit fees, and configured chain costs. This
prevents a dense event stream from inflating the number of independent opportunities.

Every source event is evaluated at most once for a strategy version. The durable cursor resumes
from the last committed event after a restart. When the processor cannot keep pace with ticker
updates, it evaluates the latest event and counts the superseded ticks instead of building an
ever-growing stale backlog.

User agents have an independent launch and pause state. A durable outbox is committed with each
paper evaluation before the research cursor advances. Assignments to the product database are
idempotent and retried until acknowledged, so a product-database outage cannot silently drop an
agent evaluation.

## Production configuration

| Variable | Service | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | both | PgBouncer connection for runtime reads and writes. |
| `DATABASE_MIGRATIONS_URL` | both | Direct private connection for schema migration. |
| `ROBINHOOD_RPC_URL` | both | Authenticated Robinhood Chain mainnet provider. |
| `RUNTIME_UNIVERSE` | collector | Validated subset of the canonical capture universe. |
| `PAPER_AGENT_CONFIG` | paper agent | Versioned strategy registration path. |
| `AAPL_MINIMUM_NET_EDGE_PPM` | paper agent and live evaluation authority | Shared reviewed production entry threshold. |
| `AGENT_DATABASE_URL` | paper agent | Direct private product-database connection for durable user-agent assignments. |
| `R2_BUCKET` | collector | Private raw-event archive. |
| `AWS_ENDPOINT_URL` | collector | Cloudflare R2 S3 endpoint. |
| `AWS_ACCESS_KEY_ID` | collector | Bucket-scoped archive credential. |
| `AWS_SECRET_ACCESS_KEY` | collector | Bucket-scoped archive credential. |
| `AWS_SESSION_TOKEN` | collector | Session token when temporary R2 credentials are used. |
| `AWS_REGION` | collector | `auto` for R2. |

Use a bucket-scoped Object Read & Write credential. Temporary credentials must be rotated before
expiry; the collector stops preserving new raw evidence when archival authentication fails and
reports the failure in its source-health stream.

## Launch verification

A production launch is complete when all of the following are observed from the deployed revision:

1. Both workers are running the same commit.
2. Lighter ticker, book, trade, and market-stat events are advancing.
3. Robinhood block and gas events are advancing through the authenticated provider.
4. Raw events are acknowledged into R2 segments and the staging backlog remains bounded.
5. Paper evaluations advance for the configured market.
6. Evaluation evidence includes a block-pinned executable spot quote, code hashes, and current multiplier state.
7. Replaying an already committed event does not create another evaluation or episode.
8. Running user agents receive one assignment per evaluation and the fanout outbox has no stale pending rows.

An open episode is not required for liveness. When the configured net edge is absent, a continuous
stream of evidence-backed declines is the correct output.

## Operator queries

```sql
SELECT source, kind, max(received_at) AS latest, count(*) AS events
FROM raw_market_events
WHERE received_at > now() - interval '15 minutes'
GROUP BY source, kind
ORDER BY source, kind;

SELECT status, coalesce(reason, 'candidate') AS outcome, count(*)
FROM paper_evaluations
WHERE evaluated_at > now() - interval '15 minutes'
GROUP BY status, outcome
ORDER BY status, outcome;

SELECT strategy_version, symbol, status, opened_at, last_observed_at, evaluation_count,
       unrealized_pnl_raw, realized_pnl_raw
FROM paper_opportunity_episodes
ORDER BY last_observed_at DESC
LIMIT 20;

SELECT count(*) AS pending_archive_events,
       min(received_at) AS oldest_pending_event
FROM event_staging
WHERE state <> 'archived';

SELECT count(*) AS pending_agent_assignments,
       min(created_at) AS oldest_pending_assignment,
       max(delivery_attempts) AS maximum_attempts
FROM agent_fanout_outbox
WHERE delivered_at IS NULL;
```

## Recovery

- A collector restart resumes capture with a new source session and reconstructs the Lighter book
  from a fresh snapshot.
- An archival upload is acknowledged only after R2 accepts the segment. Expired leases return to
  the pending queue.
- A paper-agent restart resumes from its committed cursor. Database uniqueness constraints make
  event evaluation, episode creation, and per-agent assignment idempotent. Pending agent fanout
  remains in the outbox until the product database accepts it.
- A nonce discontinuity invalidates the local Lighter book and forces reconnect rather than joining
  incompatible deltas.
