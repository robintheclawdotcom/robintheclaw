# app

The Rust product and market-data API. It authenticates Privy sessions, persists accounts in
Postgres, verifies personal-vault receipts against provider RPC, indexes application activity,
serves real dashboard state, and streams a live event feed. It never holds a user signer or signs
an owner transaction. Strategy decisions remain in the `engine` crate.

```bash
cargo run    # starts the HTTP server on 127.0.0.1:8080 by default
cargo test
```

Configuration is environment-driven (`config.rs`). Product RPC and contract addresses have no
defaults, so authenticated onchain operations remain disabled until the managed deployment is
fully configured.

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
- `product_store` — PostgreSQL users, wallet links, smart accounts, vaults, preferences, activity,
  and durable product-indexer cursors.
- `auth` and `privy` — ES256 access-token validation plus server-side recovery and wallet ownership.
- `product_indexer` — confirmed factory, vault, guard, and anchor events persisted per user.
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
| `GET /api/v1/me` | Authenticated account, linked wallets, preferences, smart account, and vault |
| `POST /api/v1/me/wallets/sync` | Refresh server-verified Privy wallet links |
| `PUT /api/v1/me/preferences` | Update display, notification, and funding-wallet preferences |
| `GET /api/v1/dashboard` | Real balances, strategy state, positions, opportunities, and activity |
| `GET /api/v1/activity?cursor=` | Cursor-paginated account activity |
| `POST /api/v1/vaults/prepare` | Deterministic sponsored onboarding call plan |
| `POST /api/v1/vaults/confirm` | Idempotent receipt and contract-state verification |
| `GET /api/v1/ws` | Authenticated live event feed |

## Execution expansion

- **Basis-scanner service**: a background loop that reads Uniswap v4 spot and the Lighter perp for
  the tradable universe, evaluates each through the `engine`, records the observation, and emits on
  the event bus. Its shape mirrors the indexer loop.
- **Signer and execution**: a key-isolated signer and the two-leg neutral executor, gated by the
  onchain `MandateGuard`.
- **Metered access**: x402 in front of the paid data endpoints.
