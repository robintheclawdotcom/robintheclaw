# Execution boundary

This crate defines the deterministic, non-networked boundary between an approved strategy and
the isolated signer services. It cannot submit a transaction or load a credential.

The first live release supports long Robinhood Stock Token spot exposure hedged by a short
Lighter perpetual. Every intent carries frozen market and dataset evidence, applies the Stock
Token multiplier, enforces the canary caps, and advances through an explicit paired-order saga.

Signer implementations must accept the typed command enums. The Lighter surface intentionally
has no transfer, withdrawal, leverage-change, key-management, or subaccount-management command.

```bash
cargo test --manifest-path execution/Cargo.toml
```
