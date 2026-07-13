# Operator control-plane operations

## Current deployment state

The repository does not yet contain a deployable operator control plane. `robin-api` is the
authenticated product API; it is not an operational read model and must not receive research,
coordinator, signer, KMS, venue, or treasury credentials. Treating it as the operator API would
collapse user-facing and capital-bearing trust boundaries.

Staged activation includes a separate private, read-only service backed by runtime and coordinator
evidence. It sits behind identity-aware access and remains excluded from every
transaction-broadcast or signing path.

## Required boundary

The operator service must:

- run as a Render private service without a public hostname;
- accept only short-lived gateway identity with operator attribution and role enforcement;
- use a dedicated read-only database role and bounded direct connection pool;
- expose source health, capture integrity, archive reconciliation, promotion state, execution saga,
  signer health, incidents, and recovery evidence;
- redact credentials, addresses not approved for disclosure, balances, live strategy parameters,
  and raw alpha data;
- produce immutable audit events for authentication, queries, acknowledgements, and recovery
  requests;
- contain no signer client, wallet library, transaction encoder, order-submission adapter, or LLM
  integration.

Database permissions are the primary write barrier. Session-level read-only settings and endpoint
authorization are additional controls, not substitutes.

## Readiness contract

Liveness proves only that the process is running. Readiness must remain unsuccessful unless:

1. the gateway identity and authorization policy are loaded;
2. the read-only database role is verified by a negative write probe;
3. required runtime, manifest, venue-event, action, incident, and promotion schemas are compatible;
4. database and R2 manifest checkpoints reconcile at the published boundary;
5. query timeouts, row limits, and concurrency limits are active;
6. metrics, traces, logs, and security audit events reach their configured sinks.

No operator route may change coordinator mode, submit an intent, invoke a signer, rotate a key,
alter a market configuration, or acknowledge a recovery action. Those operations require separate
release or recovery procedures with their own authority.

## Release procedure

1. Run the complete repository, migration, Blueprint, dependency, leak, and identity checks.
2. Review the service type, database role, gateway policy, secret references, query limits, and
   `checksPass` deployment trigger.
3. Apply schema changes through the release identity and verify backward compatibility.
4. Deploy privately and verify liveness, readiness, negative write probes, and access denial.
5. Compare source health, dataset manifests, execution state, and incident records with primary
   evidence sources.
6. Record the commit, image digest, deployment identifier, schema versions, gateway-policy digest,
   and verification results.

## Recovery drill

Run the drill quarterly and after material evidence-schema changes:

1. restore the selected PostgreSQL recovery point into an isolated private database;
2. restore or reference the matching immutable R2 manifest boundary;
3. issue new temporary database and gateway credentials;
4. verify readiness, negative write probes, table counts, source-health recency, manifests, and
   digests;
5. reconcile the restored database and R2 boundary;
6. destroy temporary credentials and resources after retaining non-sensitive evidence.

A database restore without archive reconciliation is not a completed recovery drill.

## Incident gates

| Condition | Immediate response | Recovery gate |
| --- | --- | --- |
| Readiness fails | Remove operator access and inspect private telemetry. | Schema, identity, database, and archive checks pass. |
| Unexpected authentication failures | Revoke affected gateway sessions and review audit logs. | Source is identified and policy is verified. |
| Any database write succeeds | Revoke the database credential and stop the service. | Dedicated read-only role and negative write tests pass independently. |
| Evidence diverges from R2 | Mark the dataset incomplete and stop promotion clocks. | Database and archive manifests reconcile. |
| Postgres failover | Verify connection recovery and query continuity. | Readiness and reconciliation pass on the new primary. |
| A secret appears in output | Disable access, rotate the secret, and preserve incident evidence. | Redaction fix and historical-output review are complete. |
