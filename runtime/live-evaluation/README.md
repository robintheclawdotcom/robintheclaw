# Live evaluation authority

This keyless worker converts a fresh, first-event `basis-paper-v1` AAPL candidate into an immutable entry approval. It also consumes a fresh, immutable closure of that exact paper episode and approves an unwind only for the live intent previously bound to the same account and episode. It has read-only connections to the research and product databases and a narrowly scoped execution-database role.

Before deployment, apply migrations in this order:

1. coordinator migrations through `0015_exit_execution_policy.sql`
2. both `runtime/live-scheduler/migrations` files in order
3. `runtime/live-evaluation/migrations/0001_live_evaluation.sql`
4. `runtime/live-evaluation/migrations/0002_market_config_bootstrap.sql`

The second live-evaluation migration requires the PostgreSQL `btree_gist` extension. It adds append-only review evidence and prevents overlapping market releases for one symbol.

At startup, the worker fetches the official Lighter mainnet `orderBooks` response and requires exactly one active AAPL perpetual matching the pinned market index and decimals. It then inserts the reviewed `execution_market_configs` row transactionally. Repeated and concurrent startup is idempotent; stale metadata, a changed Lighter identity, a conflicting row, or an invalid release window stops the worker. The row's `manifest_id` is the domain-separated digest produced by `MarketManifest`, so any field or validity-window change requires a new row and digest.

Required environment variables when enabled:

- `ROBIN_LIVE_EVALUATION_ENABLED=true`
- `ROBIN_LIVE_EVALUATION_RESEARCH_DATABASE_URL`
- `ROBIN_LIVE_EVALUATION_PRODUCT_DATABASE_URL`
- `ROBIN_LIVE_EVALUATION_EXECUTION_DATABASE_URL`
- `ROBIN_LIVE_EVALUATION_WORKER_ID`
- `AAPL_MINIMUM_NET_EDGE_PPM` — the same reviewed value used by the paper agent
- `ROBIN_LIVE_EVALUATION_LIGHTER_AAPL_MARKET_INDEX`
- `ROBIN_LIVE_EVALUATION_LIGHTER_AAPL_BASE_DECIMALS`
- `ROBIN_LIVE_EVALUATION_LIGHTER_AAPL_PRICE_DECIMALS`
- `ROBIN_LIVE_EVALUATION_SPOT_CONFIG_VERSION`
- `ROBIN_LIVE_EVALUATION_UI_MULTIPLIER_E18`
- `ROBIN_LIVE_EVALUATION_MAX_PRICE_DEVIATION_BPS`
- `ROBIN_LIVE_EVALUATION_MAX_UNWIND_PRICE_DEVIATION_BPS`
- `ROBIN_LIVE_EVALUATION_MARKET_VALID_FROM` — RFC 3339
- `ROBIN_LIVE_EVALUATION_MARKET_VALID_UNTIL` — RFC 3339

`ROBIN_LIVE_EVALUATION_POLL_MILLISECONDS` is optional and defaults to 250.
