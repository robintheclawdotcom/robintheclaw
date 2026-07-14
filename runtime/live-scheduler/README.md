# Live evaluation scheduler

This worker is the keyless boundary between approved source evaluations, the executable quote authority, and the live strategy runner. It has no HTTP ingestion API and accepts no market, leverage, calldata, wallet, venue credential, KMS reference, or strategy parameter from a user.

The private evaluation authority inserts one immutable `live_scheduler_approvals` row per evaluation and execution account. Entry approvals bind the opening paper episode. A completed entry dispatch records the resulting live intent, and only the matching fresh paper closure can approve an unwind. Database constraints reject extra fields and cross-account or cross-episode substitution.

Before either outbound call, the worker checks the account against the coordinator's immutable registration, active account state, global/strategy/account controls, current `canary_eligible` promotion, and coordinator readiness. It checks again after receiving a quote so an account blocked mid-flight never reaches the strategy runner.

The journal stores the stable evaluation/account dispatch identity, deterministic quote request ID, exact quote response bytes, exact strategy-runner request bytes, SHA-256 digests, every transition, and the final or ambiguous response. A retry or restarted worker reuses the persisted quote and runner request byte-for-byte. It never obtains a replacement quote for the same dispatch; an expired quote blocks the dispatch.

Apply both scheduler migrations to the coordinator database after coordinator migrations. The service starts only when `ROBIN_LIVE_SCHEDULER_ENABLED=true`.

## Render environment

- `ROBIN_LIVE_SCHEDULER_ENABLED=true`
- `ROBIN_LIVE_SCHEDULER_DATABASE_URL` — private coordinator PostgreSQL URL
- `ROBIN_LIVE_SCHEDULER_WORKER_ID` — neutral stable worker ID
- `ROBIN_QUOTE_AUTHORITY_URL`
- `ROBIN_LIVE_SCHEDULER_QUOTE_CALLER`
- `ROBIN_LIVE_SCHEDULER_QUOTE_HMAC_KEY` — base64, at least 32 bytes
- `ROBIN_QUOTE_AUTHORITY_ED25519_PUBLIC_KEY` — base64 Ed25519 public key
- `ROBIN_LIVE_SCHEDULER_LIGHTER_AAPL_MARKET_INDEX` — reviewed Lighter AAPL market index; enabled startup fails closed when absent
- `ROBIN_STRATEGY_RUNNER_URL`
- `ROBIN_LIVE_SCHEDULER_RUNNER_CALLER`
- `ROBIN_LIVE_SCHEDULER_RUNNER_HMAC_KEY` — base64, at least 32 bytes and different from the quote key

The quote and runner callers are separate service identities. The worker environment must not contain Lighter credentials, Ethereum keys, wallet material, KMS key references, transaction calldata, or strategy knobs.

Run locally:

```sh
go test ./...
go run ./cmd/live-scheduler
```
