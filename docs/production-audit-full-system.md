# Production audit: full system

## Status

This audit is subordinate to [production-audit-mainnet-readiness.md](production-audit-mainnet-readiness.md),
which contains the current release decision and gate inventory.

The typed strategy contracts are deployed halted and unfunded on Robinhood Chain mainnet. The
repository also contains durable execution-state machinery, restricted signer services, a
production-oriented research collector, and an authenticated product application. Capital
activation remains a separate promotion after these components form a closed, independently
verified trading system.

## Trust-boundary assessment

The deployed topology separates the public interface, private product API, research collector,
execution coordinator, Lighter signer, Robinhood signer, and research database. Coordinator and
signer services enter deployment disabled. Signing keys are not shared across venue boundaries, and
the coordinator holds no private key.

The product API and future operator control plane are distinct surfaces. The product API handles
authenticated user workflows and personal-vault preparation. The operator control plane does not
yet exist; when implemented, it must be private, read-only, identity-aware, and incapable of
transaction or order submission.

## Activation workstreams

- Authenticated Lighter account, order, fill, collateral, and position events are not yet wired into
  the durable reconciliation ledger.
- Canonical Robinhood Chain observations, dual-RPC reconciliation, and Ethereum-final evidence are
  not yet production-proven.
- The block-pinned Uniswap v4 execution-authority publisher and live account-risk gate do not yet
  exist as deployable services.
- The shadow/research processor, deterministic production replay, promotion artefacts, and required
  elapsed evidence windows are incomplete.
- Render, PostgreSQL, R2, production RPC, KMS, telemetry, paging, backup, and restore controls require
  account-level provisioning and retained verification evidence.
- Independent contract, execution, key, custody, legal, venue, and operational reviews remain open.

## Security assessment

The execution path is designed to fail closed around replay, authorization scope, control version,
market configuration, authoritative quotes, unknown transaction outcomes, and recovery. That design
has not yet been validated against production dependencies or an independent audit. In particular,
a compromised authenticated collector, quote publisher, RPC pair, KMS policy, or venue subaccount
could still corrupt the decision boundary if identity, freshness, and reconciliation controls are
misconfigured.

Separate HMAC keys are required for intent admission, exit and recovery, venue events, market
authority, and each signer. Startup rejects missing, malformed, or duplicate keys. The Blueprint's
secret groups express intended distribution but do not replace startup enforcement or account-level
access review.

## Reliability and performance assessment

The runtime has durable staging and archival boundaries; the coordinator uses leases, append-only
evidence, native event identities, and durable recovery actions. No production capacity claim is
justified until the one-hour 2x-peak benchmark, 24-hour chaos test, 72-hour soak, crash-boundary
tests, database failover, archive reconciliation, and deterministic replay gates produce retained
evidence.

## Release decision

The halted, unfunded typed contract deployment is complete and source-verified. Funding and a canary
remain a separate promotion governed by the complete empirical, legal, venue, audit, key, and
operational gate set.
