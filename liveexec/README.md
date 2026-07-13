# Live quote authority and strategy runner

This module contains two internal services for `basis-aapl-v1`:

- `quote-authority` obtains simultaneous executable spot and perp quotes from a reviewed adapter, pins every route and policy identity, and signs the bundle with Ed25519.
- `strategy-runner` verifies the signed quote and authenticated evaluation, readiness, and account snapshots, then submits the exact deterministic `PairIntent v2` entry to the coordinator.

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

Production activation also requires durable replay storage shared by every replica. The current inbound HMAC nonce cache is process-local and is suitable only for a single disabled or test instance.

## Coordinator persistence

Authenticated readiness and account-state fields in a runner request are proposals, not production authorization. An entry succeeds only when the coordinator independently revalidates its authoritative account snapshots, market authority, controls, promotion, and turnover, persists the exact PairIntent, and returns a bounded `201` response for the same intent in `prechecked` state at version 1. The runner verifies all response fields and never reports an intent when submission is declined or ambiguous.

The PairIntent timestamp and identifier derive from signed quote evidence, not runner wall-clock time. Retrying the same frozen request therefore cannot create a different intent. The runner sends the intent once and never automatically resends after a transport timeout, redirect, oversized response, malformed response, or response-identity mismatch.

Coordinator persistence is idempotent over the canonical full PairIntent SHA-256 digest. An exact duplicate `/v1/intents` request returns the stored saga without repeating admission, turnover reservation, or action creation. The same intent ID with a different or unverifiable digest is rejected and never treated as a duplicate.

If the create response is ambiguous, the runner does not resend. It makes one separately authenticated `/v1/intent-status` request containing the intent ID and canonical payload digest. Only `persisted` with the same digest and a valid persisted saga becomes success; the saga may already have advanced beyond `prechecked` while the response was lost. `absent`, `conflict`, `unverifiable`, another ambiguous response, or any identity mismatch remains a failure. The status read waits behind any in-flight admission for that intent ID. The status record lives in the coordinator database and is therefore shared across runner replicas; the runner does not rely on local output state.

Unwind dispatch is intentionally unavailable. Coordinator `/v1/exits` requires a `quote_source_session` and `quote_source_event_id` that identify an already persisted `/v1/market-quotes` record bound to the intent, spot amount, expected output, and deadlines. The signed quote bundle currently has source labels but no durable coordinator session/event identity or proof that such a record was accepted. The runner validates the episode-bound unwind directive and then fails closed without calling `/v1/exits`. Adding invented session or event values would bypass the coordinator's market-authority contract.

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
- `ROBIN_COORDINATOR_URL`
- `ROBIN_COORDINATOR_INTENT_CALLER`
- `ROBIN_COORDINATOR_INTENT_HMAC_KEY`

Runner ingress and quote-authority keys are base64. The coordinator intent HMAC key is exactly 32 bytes encoded as lowercase hex, matching coordinator authentication. The runner ingress and coordinator callers and HMAC keys must be distinct. Coordinator HTTP is accepted only for loopback, private IPs, or `.internal` hosts; other endpoints require HTTPS. Keys belong in the deployment secret store, never in repository files or examples.

## Validation

Run from this directory:

```sh
./scripts/validate.sh
```
