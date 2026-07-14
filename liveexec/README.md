# Live quote authority and strategy runner

This module contains two internal services for `basis-aapl-v1`:

- `quote-authority` obtains simultaneous executable spot and perp quotes from a reviewed adapter, pins every route and policy identity, and signs the bundle with Ed25519.
- `strategy-runner` verifies the signed quote and authenticated evaluation, readiness, and account snapshots, then submits the exact deterministic `PairIntent v2` entry or episode-bound exit to the coordinator.

Neither service accepts strategy code, calldata, market, leverage, threshold, or route inputs. Both services reject unknown JSON fields. The runner has no venue credential, wallet key, KMS permission, withdrawal path, or transfer path.

## Production adapter

The quote-authority binary includes the mainnet AAPL/USDG adapter and starts in enabled mode when its complete production configuration is present.

For every quote, the adapter:

- requires two independent Robinhood Chain RPC origins to agree on one finalized block;
- reads code, token decimals, pool policy, oracle state, and the exact Uniswap v4 quote at that block;
- pins the quoter, pool manager, USDG, AAPL, pool configuration, route, and runtime bytecode hashes;
- obtains executable IOC depth and mark price from Lighter's official mainnet HTTPS API;
- resolves unwind quantities and phase from the coordinator's authenticated open episode; and
- rejects RPC disagreement, stale sources, oracle failure, insufficient depth, route or code mismatch, and partial availability.

Entry and exit quote requests use distinct authenticated callers. Every exit publication binds the account, intent, unwind phase, spot quantities, perp quantity and limit, block, and deadlines before the quote is signed.

The inbound HMAC nonce cache is process-local, so the deployed quote authority runs as one replica.

## Coordinator persistence

Authenticated readiness and account-state fields in a runner request are proposals, not production authorization. An entry succeeds only when the coordinator independently revalidates its authoritative account snapshots, market authority, controls, promotion, and turnover, persists the exact PairIntent, and returns a bounded `201` response for the same intent in `prechecked` state at version 1. The runner verifies all response fields and never reports an intent when submission is declined or ambiguous.

The PairIntent timestamp and identifier derive from signed quote evidence, not runner wall-clock time. Retrying the same frozen request therefore cannot create a different intent. The runner sends the intent once and never automatically resends after a transport timeout, redirect, oversized response, malformed response, or response-identity mismatch.

Coordinator persistence is idempotent over the canonical full PairIntent SHA-256 digest. An exact duplicate `/v1/intents` request returns the stored saga without repeating admission, turnover reservation, or action creation. The same intent ID with a different or unverifiable digest is rejected and never treated as a duplicate.

If the create response is ambiguous, the runner does not resend. It makes one separately authenticated `/v1/intent-status` request containing the intent ID and canonical payload digest. Only `persisted` with the same digest and a valid persisted saga becomes success; the saga may already have advanced beyond `prechecked` while the response was lost. `absent`, `conflict`, `unverifiable`, another ambiguous response, or any identity mismatch remains a failure. The status read waits behind any in-flight admission for that intent ID. The status record lives in the coordinator database and is therefore shared across runner replicas; the runner does not rely on local output state.

Before signing an exit quote, the quote authority persists the exact account, intent, market index, manifest, route, unwind phase, spot input and output, perp base amount and limit price, block, and deadlines through authenticated `/v1/market-quotes`. The coordinator returns a digest over that canonical publication. Its durable source session, source event, sequence, digest, and deadlines are part of the signed quote bundle; the authority never invents them.

The runner submits the episode-bound exit once through authenticated `/v1/exits`. A natural strategy exit is `strategy_exit`; `reducing` and `closing` lifecycles produce `operator_exit`. If the response is ambiguous, the runner does not resend. It queries `/v1/exit-status` with the deterministic request ID and canonical payload digest and succeeds only on an exact persisted match. Exact retries are idempotent. Any source-identity or payload collision halts the affected account and global execution and records a critical incident.

## Fixed policy

The protocol pins:

- strategy `basis-aapl-v1`;
- manifest `4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a`;
- Robinhood Chain `4663`, AAPL/USDG, and the checked-in router;
- a reviewed Lighter mainnet AAPL market index supplied explicitly to both services;
- a Lighter trading API key index from `4` through `254`; indices `0` through `3` are reserved;
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

- `ROBIN_QUOTE_AUTHORITY_ENABLED=true`
- `ROBIN_QUOTE_AUTHORITY_LISTEN=:8080`
- `ROBIN_QUOTE_AUTHORITY_CALLER`
- `ROBIN_QUOTE_AUTHORITY_HMAC_KEY`
- `ROBIN_QUOTE_AUTHORITY_EXIT_CALLER`
- `ROBIN_QUOTE_AUTHORITY_EXIT_HMAC_KEY`
- `ROBIN_QUOTE_AUTHORITY_ED25519_PRIVATE_KEY`
- `ROBIN_LIGHTER_AAPL_MARKET_INDEX`
- `LIGHTER_AAPL_BASE_DECIMALS`
- `LIGHTER_AAPL_PRICE_DECIMALS`
- `LIGHTER_API_URL=https://mainnet.zklighter.elliot.ai`
- `ROBIN_COORDINATOR_URL`
- `ROBIN_COORDINATOR_MARKET_CALLER`
- `ROBIN_COORDINATOR_MARKET_HMAC_KEY`
- `ROBIN_COORDINATOR_EPISODE_CALLER`
- `ROBIN_COORDINATOR_EPISODE_HMAC_KEY`
- `ROBINHOOD_RPC_URL`
- `ROBINHOOD_RECONCILIATION_RPC_URL`
- `AAPL_REFERENCE_FEED`
- `AAPL_REFERENCE_FEED_CODE_HASH`
- `AAPL_REFERENCE_FEED_DECIMALS=8`
- `AAPL_REFERENCE_FEED_HEARTBEAT_SECONDS=90000`

Strategy runner:

- `ROBIN_STRATEGY_RUNNER_ENABLED=true`
- `ROBIN_STRATEGY_RUNNER_LISTEN=:8080`
- `ROBIN_STRATEGY_RUNNER_CALLER`
- `ROBIN_STRATEGY_RUNNER_HMAC_KEY`
- `ROBIN_QUOTE_AUTHORITY_ED25519_PUBLIC_KEY`
- `ROBIN_COORDINATOR_URL`
- `ROBIN_COORDINATOR_INTENT_CALLER`
- `ROBIN_COORDINATOR_INTENT_HMAC_KEY`
- `ROBIN_COORDINATOR_EXIT_CALLER`
- `ROBIN_COORDINATOR_EXIT_HMAC_KEY`
- `ROBIN_LIGHTER_AAPL_MARKET_INDEX`

There is no default Lighter market index. Enabled services require the exact configured index, and both reject a signed quote whose index differs even when the account snapshot repeats the same value.

The reference address is the deterministic Robinhood Chain `QuorumAaplReferenceFeed`, not the
Arbitrum proxy. Three relay workers read Chainlink AAPL/USD at a common finalized Arbitrum block,
preserve the source round and timestamp, and publish exact 2-of-3 consensus. Relay evidence expires
after 60 seconds; the preserved source round expires after 25 hours. Executable venue quotes still
expire after five seconds.

Runner ingress and quote-authority entry and exit keys are base64. Coordinator HMAC keys are exactly 32 bytes encoded as lowercase hex. Quote entry, quote exit, market publication, and episode resolution each use a distinct caller and key. Coordinator HTTP is accepted only for loopback, private IPs, or `.internal` hosts; other endpoints require HTTPS. Keys belong in the deployment secret store, never in repository files or examples.

## Validation

Run from this directory:

```sh
./scripts/validate.sh
```
