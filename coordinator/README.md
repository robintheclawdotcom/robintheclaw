# Execution coordinator

The coordinator persists approved pair intents and every lifecycle transition. It admits an
intent only when the strategy has complete canary-promotion evidence and the execution account has
no active episode.

The service starts disabled and reports unready until its database schema and both private signer
services are available. It does not run migrations at startup and cannot bypass the promotion
gate. The database migration must run as a separate release step.

Intent admission is idempotent over the SHA-256 digest of the canonical full `PairIntent` payload.
An exact retry returns the stored saga without repeating admission or reserving turnover. A payload
digest mismatch for an existing intent ID halts global and account execution and records a critical
incident.

`POST /v1/intent-status` uses the intent HMAC scope and accepts only `intent_id` plus
`payload_sha256`. It returns `persisted` with the stored saga only when both values match. `absent`,
`conflict`, and `unverifiable` never authorize execution. Rows created before migration `0009` have
an explicit legacy marker and remain unverifiable rather than being inferred from JSONB text. The
database requires a digest for every new row. Status reads take the same per-intent transaction lock
as admission, so `absent` cannot race an earlier in-flight commit.

Exit quotes use the market HMAC scope (`MARKET_QUOTE_CALLER_ID` and
`COORDINATOR_MARKET_HMAC_KEY`). A version-1 exit publication must bind its execution account,
intent, strategy and route digests, reviewed Lighter market index, quantities, executable output,
and submission and reconciliation deadlines. The coordinator verifies the quoted index against
both the stored PairIntent and the reviewed `execution_market_configs` row before returning the
canonical publication digest.

Lighter API key indices `0` through `3` are reserved. Account registration, snapshots, nonce
assignment, signer responses, and database constraints accept only indices `4` through `254`.

`POST /v1/exits` and `POST /v1/exit-status` use the exit HMAC scope. Exit request IDs are
idempotent over the canonical request digest. Exact retries return the current saga without
creating another action; conflicting payloads and market-source collisions halt global execution
and every affected account and create critical incidents.

```bash
cargo test --manifest-path coordinator/Cargo.toml
sqlx migrate run --source coordinator/migrations
```
