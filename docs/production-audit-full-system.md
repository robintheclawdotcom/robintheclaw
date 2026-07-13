# Production Audit: full system

## Executive summary

The repository is not ready to trade or custody mainnet capital. This hardening increment makes
the infrastructure honest about that fact: the operator API is read-only and durable, unimplemented
signer and shadow services are absent from deployment, production configuration fails closed, and
CI now covers every top-level package plus supply-chain and contract-security checks. The remaining
P0 work is substantive engineering and independent review, not deployment configuration.

## Critical issues (P0 - block capital activation)

- [ ] Typed, audited on-chain custody and spot execution are not yet released. The generic v0 path
  must never be funded.
- [ ] The durable shadow lifecycle and promotion evidence windows are incomplete. Mainnet capital
  cannot be activated before the documented 180-day capture and 60-day continuous-shadow gates.
- [ ] Lighter and Robinhood signer services, durable execution saga, nonce recovery, dead-man
  cancellation, and emergency unwind are not implemented. They remain absent from the Blueprint.
- [ ] Independent contract, executor, custody, key-management, and legal reviews are outstanding.
- [ ] Production Robinhood archive/WebSocket RPC, bucket-scoped R2 credentials, retention locks,
  and end-to-end archive reconciliation require account-level provisioning.
- [ ] Safe administration, timelock, guardian, and KMS ownership ceremonies have not occurred.

## High priority (P1 - block technical readiness)

- [ ] Provision the Render Blueprint and verify its private networking, Pro database, HA standby,
  PgBouncer, storage autoscaling, PITR, and rollback behavior.
- [ ] Restore Render billing and bind the existing site and domain to this Blueprint. Account-aware
  validation is currently blocked by billing suspension and the unbound existing domain.
- [ ] Create a dedicated read-only Postgres role for the control API. Session-level read-only mode
  is a second control, not a substitute for database permissions.
- [ ] Connect authenticated access, OpenTelemetry export, paging, and audit-log retention.
- [ ] Add compatibility tests that run the control API against each runtime migration sequence.
- [ ] Execute the one-hour throughput, 24-hour chaos, 72-hour soak, restore, and R2 reconciliation
  gates and retain immutable evidence.
- [ ] Enable protected `main` with required CI, CodeQL, and identity-firewall checks.
- [ ] Upgrade the runtime dependency chain until RustSec reports no `quick-xml` or optional
  SQLx/MySQL advisory. The dependency job intentionally blocks while either remains in its lock.

## Medium priority (P2 - complete before operator scale-up)

- [ ] Add a read replica only after query telemetry demonstrates primary contention.
- [ ] Replace the static control token with short-lived gateway identity once the operator access
  model is chosen; retain service-to-service authentication.
- [ ] Define versioned API compatibility and deprecation policy before external consumers exist.
- [ ] Add cardinality budgets and retention policy for metrics, traces, and logs.

## Low priority (P3 - technical debt)

- [ ] Move independent crate locks into a root workspace only when release cadence and dependency
  ownership are intentionally unified.
- [ ] Add generated OpenAPI output after endpoint contracts stabilize.

## Security assessment

The former backend combined a public HTTP surface, a mutable in-memory market view, a chain
indexer, event bus, and raw-transaction broadcast method. That boundary was unsuitable for an
operator control plane. This increment removes those modules and the engine dependency. All
operator queries now come from Postgres, protected routes use constant-time bearer comparison,
database sessions are read-only with statement timeouts, and response errors do not expose
dependency details.

This is still defense in depth rather than a complete access system. A bearer token does not
provide operator identity, phishing resistance, device posture, or granular authorization. The
service must remain private until an authenticated gateway is reviewed and configured.

## Performance assessment

The API bounds list queries and capture windows, caps its direct pool, and applies a database
statement timeout. No material load claim is justified until runtime tables are
partitioned, representative data is loaded, and query plans are captured. The control plane must
not compete with ingestion for primary database resources.

## Observability assessment

Liveness, database/schema readiness, request counts, rejected authentication, and database
failures now exist. Export, dashboards, SLOs, and paging remain external work. Logging deliberately
excludes tokens, database URLs, query results, and request headers.

## Recommended architecture changes

- Keep runtime as the only market and chain evidence writer.
- Keep the control API read-only and private.
- Give collector, shadow, control, execution, and signer services separate database roles and
  credentials with minimum authority.
- Do not add execution or signer declarations until each service has a fail-closed startup gate,
  health model, threat model, and tested recovery procedure.
- Treat R2 manifests and Postgres as a reconciled evidence pair rather than interchangeable stores.

## Test coverage gaps

- Database integration tests require an ephemeral Postgres instance and all runtime migrations.
- Authentication middleware needs HTTP-level tests in addition to constant-time comparison tests.
- Render deployment, database failover, token rotation, gateway denial, and restore behavior need
  environment-level tests.
- Security scanners require baselines and reviewed suppressions; a green scan is not an audit.

## Action plan

1. Merge and enforce the repository policy, Blueprint validation, package discovery, dependency
   audits, Slither/Aderyn, SBOM, provenance, leak, and identity checks.
2. Complete the typed contracts, durable runtime, shadow engine, and replay evidence in parallel.
3. Provision the non-signing data plane and run database/R2 recovery and soak gates.
4. Complete signer and custody reviews before adding those private services to the Blueprint.
5. Deploy audited contracts halted and unfunded only after technical readiness evidence is signed.
6. Keep capital activation blocked until every empirical, legal, audit, and operating gate passes.
