# Audits

Security reviews of the Robin the Claw contracts and execution system are collected here.

The release audit is performed in-repository and records its exact commit, scope, tests, findings,
and remediations. It is the audit gate for release. The current review is
[production-audit-mainnet-live-execution.md](../docs/production-audit-mainnet-live-execution.md).

[scope-v1.md](scope-v1.md) defines the contract and execution invariants for that review. Every
release must reference an audit covering its exact source and reproducible bytecode.
