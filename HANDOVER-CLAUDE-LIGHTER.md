# Handover: Lighter public-feed hardening

Base commit: 8f4ec86
Branch: claude/lighter-public-feed

## Objective

Harden the read-only Lighter public connector against documented production protocol behavior.
Do not add authentication, signing, order submission, storage migrations, or strategy logic.

Work from a clean worktree. Commit as `robintheclaw` with the configured neutral noreply address.
Do not merge into main.

## Owned files

- runtime/src/lighter.rs
- runtime/tests/lighter_feed.rs
- runtime/fixtures/lighter/**
- docs/venue-lighter.md
- runtime/Cargo.toml only if a test-only dependency is required

Do not edit collector.rs, storage.rs, migrations, engine, contracts, app, config, render.yaml,
existing workflows, or unrelated documentation.

## Required behavior

- Deserialize order-book, ticker, trade, market-stats, height, subscription acknowledgement,
  and error frames through typed versioned structures.
- Preserve top-level millisecond timestamps and microsecond `last_updated_at` independently.
- Parse current and settled funding, funding timestamp, open interest, mark, index, bid, ask,
  market precision, minimum size, margin fractions, and fee fields without lossy defaults.
- Reconstruct the complete order-book snapshot and subsequent deltas.
- Require each update's `begin_nonce` to equal the previous update's `nonce`.
- On a gap, invalidate the local book and return a typed reconnect-required error.
- Validate subscription acknowledgements and reject malformed required fields.
- Enforce the subscription budget `4 * market_count + 1 <= 100`.
- Send a WebSocket ping every 60 seconds.
- Use capped exponential reconnect backoff with jitter.
- Store reviewed official example payloads as golden fixtures.
- Tests must not call the network.

## Acceptance

- `cargo fmt --check`
- `cargo clippy --all-targets -- -D warnings`
- `cargo test`
- Golden tests cover snapshot, valid delta, nonce gap, ticker, trade, market stats, height,
  malformed required fields, acknowledgement failure, and subscription-budget overflow.
- No secret, wallet path, signer dependency, authenticated endpoint, or write API appears in
  the diff.
- Run the repository leak and identity checks.
- Deliver one focused commit plus a note listing changed files, test results, and documented
  schema ambiguities.
