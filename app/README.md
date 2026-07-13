# app

The backend service. It exposes the HTTP API, indexes Robinhood Chain, runs background services,
and streams a live event feed. Pure infrastructure: it holds no strategy parameters and executes
no trades. The decision logic lives in the `engine` crate, which this crate depends on.

```bash
cargo run    # starts the HTTP server on 127.0.0.1:8080 by default
cargo test
```

Configuration is environment-driven (`config.rs`); the defaults target Robinhood Chain mainnet and
leave contract addresses empty so a fresh process does not index the wrong chain.

## Modules

- `config` — environment-driven runtime configuration (RPC endpoints, chain id, watched contract
  addresses, indexer parameters, toggles).
- `evm::rpc` — JSON-RPC client with deduplicated read-endpoint failover on rate-limit and transport
  errors; broadcasts never fail over, so a transaction is never submitted twice.
- `evm::indexer` — reorg-safe log indexer: reads only to a confirmations-deep target, dedups by
  (topic, tx hash, log index), keeps logs newest-first, and caps the retained set.
- `event_bus` — tokio broadcast of the basis-arb lifecycle (`BasisObserved`, `TradePlanned`,
  `LegFilled`, `PositionClosed`, `AgentHalted`).
- `ws` — the live feed: a broadcast hub plus a WebSocket session that streams every event to the
  client.
- `store` — in-memory application state (chain-sync cursor, recent basis observations).
- `orchestrator` — background service lifecycle: the indexer loop (with a 20/60/120/240/300s
  rate-limit backoff ladder, persisting its cursor to the store) and the event-to-feed bridge.
- `api` — HTTP routes: `/health`, `/api/basis`, `/api/evm/status`, `/api/evm/logs`, and `/ws`.

## Endpoints

| Route | Purpose |
| --- | --- |
| `GET /health` | Status, chain id, current head, indexer cursor |
| `GET /api/basis?limit=N` | Recent basis observations, newest first |
| `GET /api/evm/status` | Indexer cursor and watched addresses |
| `GET /api/evm/logs?limit=N` | Recent indexed logs |
| `GET /ws` | Live event feed (WebSocket) |

## Later phases

- **Store**: swap the in-memory store for a Postgres-backed one behind the same methods (durable
  cursors and observation history).
- **Basis-scanner service**: a background loop that reads Uniswap v4 spot and the Lighter perp for
  the tradable universe, evaluates each through the `engine`, records the observation, and emits on
  the event bus. Its shape mirrors the indexer loop.
- **Signer and execution**: a key-isolated signer and the two-leg neutral executor, gated by the
  on-chain `MandateGuard`.
- **Metered access**: x402 in front of the paid data endpoints.
