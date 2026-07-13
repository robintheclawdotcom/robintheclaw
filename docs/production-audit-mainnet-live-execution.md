# Production audit: mainnet live execution

Date: 2026-07-13  
Scope: per-user onboarding, custody, signing, execution, reconciliation, and rollout for `basis-aapl-v1`

## Executive summary

Robin is not ready to trade customer funds on mainnet. The repository now has a materially stronger fail-closed foundation, but several services, external approvals, audits, and observation gates are still absent. All capital and execution flags remain disabled. No contract deployment or live trade was performed as part of this work.

Production-readiness score: **3/10 for live capital, 7/10 for the disabled foundation**.

The critical architectural correction is in place: gas sponsorship is not an ownership or launch dependency. An owner or ordinary relayer can pay deployment gas, and execution signers use separately funded addresses. Alchemy remains optional.

## Implemented controls

- Pair intent version 2 binds execution account, agent, evaluation, strategy and risk versions, Lighter identity, Robinhood vault, and Robinhood signer. Entry and unwind identifiers are deterministic and domain-separated.
- Coordinator storage, nonces, controls, snapshots, venue events, turnover, and active episodes are account-scoped. Legacy singleton state migrates blocked.
- Entry admission requires fresh authenticated state, account and signer binding matches, margin coverage, funding, finality, reconciliation, and active controls.
- Product lifecycle data is server-controlled. Commands are durable and remain pending until a reconciled worker returns evidence; an API request no longer claims that launch, unwind, close, or withdrawal already happened.
- Readiness is derived from a complete append-only evidence snapshot. Volatile checks expire within 60 seconds; linkage and deployment evidence expire within 24 hours. Missing or stale evidence fails closed.
- Lighter credentials are generated in a private provisioner and envelope-encrypted with account-bound AES-256-GCM data. The product database receives only public linkage material.
- Per-user custody uses a non-upgradeable deterministic vault, risk manager, and fixed-route adapter graph. Owner withdrawal authority is not delegated to the agent, relayer, guardian, or governance.
- The execution registry starts halted. Factory approval requires timelock action. Guardian authority is restrict-only.
- Sequencer health requires three distinct publishers, 2-of-3 agreement, and evidence no older than 60 seconds.
- The canonical strategy manifest pins the source, route, oracle, risk, and code artifacts. Activation policy and product execution accounts bind its checksum.
- V1 policy caps are fixed at $25 per leg, $50 gross, 1x exposure, one active episode, and $50 daily turnover. Both entry and exit notional consume turnover.
- The deployment script is deterministic, rejects mismatched code and role configuration, leaves the registry halted and factory unapproved, and outputs timelock calldata without broadcasting it.

## P0 launch blockers

| Blocker | Required exit condition |
|---|---|
| Independent contract and executor audits | Final reports have no unresolved critical or high findings; deployed bytecode reproduces the reviewed release. |
| Legal and venue approval | Written approval covers the exact internal canary, custody model, Lighter API-key association, and Robinhood Chain route. |
| AAPL reference feed | Reviewed production feed is deployed, monitored, and bound to the released factory. |
| Oracle and sequencer publishers | Three independent health publishers operate with tested 2-of-3 quorum, stale-data rejection, alerting, and restore procedures. |
| Authenticated venue publishers | Lighter account state and dual-RPC Robinhood finality are reconstructed continuously, including gaps, reorgs, and RPC disagreement. |
| Executable quote authority and live runner | Keyless runner consumes only the promoted manifest and emits PairIntent v2 from fresh executable quotes. |
| Dynamic Robinhood key provisioning | Each execution account receives a distinct non-exportable KMS key and an authoritative signer/vault/code-hash binding without static registry files. |
| Command execution worker | Product outbox commands drive coordinator controls, unwind, revocation, owner-signature preparation, and reconciled completion without ambiguous replay. |
| Exit proof | A production-sized exit is executable through every dependency, including the bounded reduce-only repair path. |
| Operations | Paging and dashboards cover unhedged duration, source age/gaps, margin, unknown positions, nonce drift, signer/KMS failures, finality, outbox lag, and gas. |
| Capital-policy evidence | The existing 180 verified capture days and 60 continuous shadow days complete, or a signed engineering-canary amendment is approved for at most the two internal accounts. |

No deployment flag may be enabled merely because code tests pass. The current policy file records every external gate as open.

## P1 gaps

- Robinhood onboarding still needs a private KMS-key and deterministic-graph provisioner connected to the product prepare/confirm flow.
- Eligibility and jurisdiction decisions need an authoritative service and immutable decision evidence. A client-supplied boolean is not acceptable.
- Strategy promotion needs signed operator evidence for each state transition and automatic reset of the clean-observation clock on a stage-failing incident.
- Product and coordinator command stores need an end-to-end dispatcher with delivery leases, explicit ambiguity states, and recovery drills.
- Withdrawal must remain an owner-signed chain call prepared only after both venues reconcile flat.
- Account, strategy, and global control changes need audited operator tooling and separation of duties.
- Database failover, KMS rotation, RPC degradation, and pause-during-every-saga-phase tests must run in a production-like environment.

## Security assessment

### Strengths

- Customer funds are isolated by owner-specific non-upgradeable graphs.
- Signer requests are scoped by execution account and reject returned identity mismatches.
- Lighter owner association does not expose an Ethereum private key.
- Readiness and command histories are append-only or evidence-backed.
- Cross-tenant public identifiers, Lighter accounts, public keys, proof transactions, and Robinhood vaults are database-unique once verification starts.
- Activation requires an exact policy schema. Missing or renamed gates and unknown fields are rejected.

### Remaining risks

- Static signer registries are not acceptable for public onboarding; both venue signers require authoritative dynamic account resolution.
- Publisher compromise remains a material risk until independent sources, quorum, replay protection, and source-specific keys are deployed and drilled.
- The system has not undergone an independent cross-tenant substitution review.
- There is no production incident history or verified recovery-time evidence.

## Reliability and observability

The coordinator contains crash-safe saga and account-scoped reconciliation primitives, but production reliability is unproven. Required exercises include websocket loss with REST reconstruction, ambiguous sends, crash after signing, database failover, RPC disagreement, chain reorg, signer key rotation, rate limiting, and pause at every saga phase.

Every deployment must expose disabled, live, and ready health separately. A process being alive is not evidence that it may accept capital or entries.

## Verification completed in this branch

- Contract unit, fuzz, invariant, deployment, isolation, owner-consent, sequencer-quorum, turnover, and recovery suites.
- Rust unit and PostgreSQL migration/integration tests for account-scoped execution, fresh readiness, append-only evidence, and tenant uniqueness.
- Go provisioner and signer unit, race, vet, build, replay, mismatch, and rotation tests.
- Web unit, type, build, and onboarding lifecycle checks.
- Exact activation-policy, release-manifest, Render blueprint, identity, and leak checks.

These tests validate disabled software behavior. They do not satisfy audits, venue approval, oracle review, historical capture, shadow operation, or live exit proof.

## Release decision

**No-go for customer or operator capital today.**

The next permitted milestone is a disabled deployment of migrations, services, registry, quorum feed, and factory candidate. After independent audit and external gates pass, the existing singleton may run a $25-per-leg operator canary only if the current capital policy is satisfied or the narrowly scoped engineering-canary amendment is signed. Public onboarding remains blocked until the personal canary and cohort gates complete.
