# Live quote authority and strategy runner

This module contains two internal services for `basis-aapl-v1`:

- `quote-authority` obtains simultaneous executable spot and perp quotes from a reviewed adapter, pins every route and policy identity, and signs the bundle with Ed25519.
- `strategy-runner` verifies the signed quote and authenticated evaluation, readiness, and account snapshots before emitting a deterministic `PairIntent v2` entry or an episode-bound reduce-only unwind directive.

Neither service accepts strategy code, calldata, market, leverage, threshold, or route inputs. Both services reject unknown JSON fields. The runner has no venue credential, wallet key, KMS permission, withdrawal path, or transfer path.

## Safety state

Both services default to disabled. The quote-authority binary refuses to start in enabled mode because the repository does not yet contain a reviewed production adapter for executable Robinhood Chain AAPL/USDG quotes and authenticated Lighter AAPL order-book quotes. Synthetic adapters exist only in tests.

Do not remove that startup block until the adapter:

- resolves the canonical account and route itself from `executionAccountId`;
- simulates the exact Uniswap v4 call against a pinned block from both configured RPCs;
- obtains an authenticated, executable Lighter IOC price and quantity;
- returns source identities, the reference-oracle round, block hash, and a validity interval of at most five seconds;
- fails on RPC disagreement, order-book gaps, stale data, route/code-hash mismatch, or partial source availability; and
- has independent review plus mainnet-fork and venue integration coverage.

Production activation also requires durable replay storage shared by every replica. The current HMAC nonce cache is process-local and is suitable only for a single disabled or test instance. The coordinator must accept the runner's authenticated output and persist the evaluation/quote evidence before either binary is enabled.

## Fixed policy

The protocol pins:

- strategy `basis-aapl-v1`;
- manifest `4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a`;
- Robinhood Chain `4663`, AAPL/USDG, and the checked-in router;
- Lighter mainnet AAPL;
- long spot / short perp entry and reduce-only inverse unwind;
- $25 per leg, $50 gross, 1x maximum exposure, one active episode, and $50 daily turnover;
- fresh authenticated state and executable quotes no older than five seconds; and
- 2x maintenance-margin coverage for entry.

The Go `PairIntent` field order matches the Rust serializer because the field order is part of the domain-separated identifier. Focused verification cross-deserializes a generated runner payload with `execution`'s `validate-intent` binary.

## Authentication

Internal requests use these headers:

- `X-Robin-Caller`
- `X-Robin-Timestamp`
- `X-Robin-Nonce`
- `X-Robin-Signature`

The signature is HMAC-SHA256 over the method, path, caller, timestamp, nonce, and SHA-256 body digest, separated by newlines. Timestamps have a 30-second skew limit. Nonces cannot be reused during their five-minute retention window.

Quote bundles have a domain-separated SHA-256 ID and Ed25519 signature. The runner trusts one configured public key and rejects an embedded-key mismatch before evaluating readiness.

## Configuration

Quote authority:

- `ROBIN_QUOTE_AUTHORITY_ENABLED=false`
- `ROBIN_QUOTE_AUTHORITY_LISTEN=:8080`
- `ROBIN_QUOTE_AUTHORITY_CALLER`
- `ROBIN_QUOTE_AUTHORITY_HMAC_KEY`
- `ROBIN_QUOTE_AUTHORITY_ED25519_PRIVATE_KEY`

Strategy runner:

- `ROBIN_STRATEGY_RUNNER_ENABLED=false`
- `ROBIN_STRATEGY_RUNNER_LISTEN=:8080`
- `ROBIN_STRATEGY_RUNNER_CALLER`
- `ROBIN_STRATEGY_RUNNER_HMAC_KEY`
- `ROBIN_QUOTE_AUTHORITY_ED25519_PUBLIC_KEY`

All key values are base64. They belong in the deployment secret store, never in repository files or examples.

## Validation

Run from this directory:

```sh
./scripts/validate.sh
```
