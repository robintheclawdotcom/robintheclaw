# Execution coordinator

The coordinator persists approved pair intents and every lifecycle transition. It admits an
intent only when the strategy has complete canary-promotion evidence and no active episode exists
for the same strategy, symbol, and direction.

The service starts disabled and reports unready until its database schema and both private signer
services are available. It does not run migrations at startup and cannot bypass the promotion
gate. The database migration must run as a separate release step.

```bash
cargo test --manifest-path coordinator/Cargo.toml
sqlx migrate run --source coordinator/migrations
```
