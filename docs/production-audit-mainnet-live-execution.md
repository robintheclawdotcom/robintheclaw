# Production audit: mainnet live execution

Date: 2026-07-14
Scope: per-user onboarding, custody, signing, execution, reconciliation, and rollout for `basis-aapl-v1`

## Executive summary

Robin is not ready to trade customer funds on mainnet. The repository now has a materially stronger fail-closed foundation, but several services, external approvals, audits, and observation gates are still absent. All capital and execution flags remain disabled. No contract deployment or live trade was performed as part of this work.

Production-readiness score: **3/10 for live capital, 8/10 for the disabled foundation**.

The critical architectural correction is in place: gas sponsorship is not an ownership or launch dependency. An owner or ordinary relayer can pay deployment gas, and execution signers use separately funded addresses. Alchemy remains optional.

## Implemented controls

- Pair intent version 2 binds execution account, agent, evaluation, strategy and risk versions, Lighter identity, Robinhood vault, and Robinhood signer. Entry and unwind identifiers are deterministic and domain-separated.
- Coordinator storage, nonces, controls, snapshots, venue events, turnover, and active episodes are account-scoped. Legacy singleton state migrates blocked.
- Entry admission requires fresh authenticated state, account and signer binding matches, margin coverage, funding, finality, reconciliation, and active controls.
- Product lifecycle data is server-controlled. Commands are durable and remain pending until a reconciled worker returns evidence; an API request no longer claims that launch, unwind, close, or withdrawal already happened.
- Readiness is derived from a complete append-only evidence snapshot. A ready decision requires volatile evidence no older than five seconds; linkage and deployment evidence expire within 24 hours. Missing, future-dated, or stale evidence fails closed.
- Lighter credentials are generated in a private provisioner and envelope-encrypted with account-bound AES-256-GCM data. The product database receives only public linkage material.
- Robinhood execution keys are generated as distinct non-exportable secp256k1 KMS keys. The private provisioner verifies the deterministic graph through two RPC endpoints before exposing a canonical public binding.
- Per-user custody uses a non-upgradeable deterministic vault, risk manager, and fixed-route adapter graph. Owner withdrawal authority is not delegated to the agent, relayer, guardian, or governance.
- The execution registry starts halted. Factory approval requires timelock action. Guardian authority is restrict-only.
- Sequencer health requires three distinct publishers, 2-of-3 agreement, and evidence no older than 60 seconds.
- The canonical strategy manifest pins the source, route, oracle, risk, and code artifacts. Activation policy and product execution accounts bind its checksum.
- V1 policy caps are fixed at $25 per leg, $50 gross, 1x exposure, one active episode, and $50 daily turnover. Both entry and exit notional consume turnover.
- The deployment script is deterministic, rejects mismatched code and role configuration, leaves the registry halted and factory unapproved, and outputs timelock calldata without broadcasting it.
- Disabled Lighter and Robinhood publishers discover registered accounts dynamically and reconstruct account, order, position, collateral, nonce, graph, receipt, funding, gas, and finality evidence without exporting signer credentials. They never assert that capital policy is active.
- The executable quote authority and keyless runner validate the exact promoted manifest and PairIntent v2 contract. Enabled startup fails until a production quote adapter is configured.
- A keyless scheduler durably leases approved evaluations, rechecks promotion and account controls, and invokes the quote authority and runner with separate authenticated identities.
- Exit quotes and unwind dispatch are durable and idempotent. Pause, close, natural exit, and bounded repair still require production adapter and recovery proof.
- The product command dispatcher uses durable leases, authenticated coordinator requests, explicit ambiguity handling, and owner-only unsigned withdrawal actions after flat reconciliation.
- The browser recovers and polls pending launch, pause, resume, close, and withdrawal commands, refreshes terminal state, and persists owner transaction hashes at submission time rather than after receipt confirmation.
- Linked public venue and custody bindings enter the coordinator through a durable product outbox and an authenticated, immutable registration contract. Product readiness and every command remain blocked until that registration succeeds.
- Intent persistence stores a canonical payload digest. Exact duplicates are idempotent, lost responses are resolved through an authenticated status query, and payload collisions halt account and global execution with a critical incident.
- The browser submits only the provisioner-authorized `deploy(owner)` call on chain 4663, pays ordinary ETH gas, persists its transaction hash across the 64-block finality wait, and confirms with no client-supplied graph address.
- Owner withdrawal actions are checked against the immutable registered owner and vault, exact approved selectors, and action order in both the product backend and browser before wallet presentation.
- The operations package defines the required metric contract, alert rules, dashboard, and kill-path drill. It is not yet connected to production metric producers or paging.
- Promotion changes require signed, append-only operator evidence. A separate signed operator tool can only restrict global, strategy, or account controls; it cannot activate execution or move funds.

## P0 launch blockers

| Blocker | Required exit condition |
|---|---|
| Independent contract and executor audits | Final reports have no unresolved critical or high findings; deployed bytecode reproduces the reviewed release. |
| Legal and venue approval | Written approval covers the exact internal canary, custody model, Lighter API-key association, and Robinhood Chain route. |
| AAPL reference feed | Reviewed production feed is deployed, monitored, and bound to the released factory. |
| Oracle and sequencer publishers | Three independent health publishers operate with tested 2-of-3 quorum, stale-data rejection, alerting, and restore procedures. |
| Dynamic authenticated publisher deployment | Deploy the implemented registered-account discovery and signer-private, account-bound Lighter reads. Prove new-account pickup, blocked-account removal, durable cursor reconstruction, reorg handling, and paging. The official Lighter interfaces do not currently expose a documented cross-channel contiguous sequence, so REST reconstruction remains authoritative. |
| Executable quote adapter | Connect the implemented authority and keyless runner to reviewed production AAPL and Lighter executable-quote sources. Enabled startup currently fails closed because no approved live adapter exists. |
| Evaluation authority and scheduler deployment | Deploy the implemented keyless scheduler and add the private authority that writes approved evaluations. Prove exact account admission, lease recovery, stale-input rejection, and production failover. |
| Executable unwind proof | Connect the implemented durable exit path to the reviewed production adapters and prove natural, pause, close, and bounded-repair unwinds under ambiguity and failover. |
| Activation and restriction control | Add a deployable, attributed activation path that cannot bypass readiness. Deploy and drill the implemented signed restrict-only operator tool with separate credentials and database privileges. |
| Runner replay and failover | Move inbound request-nonce state to shared durable storage or prove single-replica fencing. Intent persistence itself is now idempotent and queryable, but the runner's inbound nonce cache remains process-local. |
| Canonical account registration deployment | Deploy the implemented product outbox and coordinator registration APIs, then prove failover, exact retries, substitutions, and conflict-triggered halt behavior against production-like databases. |
| Dynamic Robinhood provisioning deployment | Deploy the implemented private KMS and deterministic-graph provisioner with audited chain-4663 factory, registry, bytecode, policy, and timelocked agent-approval values. |
| Command worker deployment | Deploy the implemented command dispatcher, exercise every coordinator ambiguity path, and prove owner-signed close and withdrawal behavior after flat reconciliation. |
| Human-usable mainnet onboarding | Replace manual Lighter account index and nonce entry with owner-side creation or discovery, verify that the subaccount is new and flat, expose both venue funding actions and exact addresses, and provide recovery for rejected and pre-registration close states. |
| Complete capital recovery | Show and verify both the Robinhood owner withdrawal and the separate secure Lighter USDC withdrawal path. The current browser action covers only the Robinhood vault. |
| Exit proof | A production-sized exit is executable through every dependency, including the bounded reduce-only repair path. |
| Operations | Connect the implemented metrics, alerts, and dashboard to production producers and paging; execute and retain evidence for global, strategy, account, guardian, and user kill drills. |
| Capital-policy evidence | The existing 180 verified capture days and 60 continuous shadow days complete, or a signed engineering-canary amendment is approved for at most the two internal accounts. |

No deployment flag may be enabled merely because code tests pass. The current policy file records every external gate as open.

## P1 gaps

- Eligibility and jurisdiction decisions need an authoritative service and immutable decision evidence. A client-supplied boolean is not acceptable.
- Withdrawal must remain an owner-signed chain call prepared only after both venues reconcile flat.
- Database failover, KMS rotation, RPC degradation, and pause-during-every-saga-phase tests must run in a production-like environment.
- The application must present one unambiguous network and environment. Mainnet wallet actions cannot coexist with a shell and dashboard labeled testnet.

## Security assessment

### Strengths

- Customer funds are isolated by owner-specific non-upgradeable graphs.
- Signer requests are scoped by execution account and reject returned identity mismatches.
- Lighter owner association does not expose an Ethereum private key.
- Readiness and command histories are append-only or evidence-backed.
- Cross-tenant public identifiers, Lighter accounts, public keys, proof transactions, and Robinhood vaults are database-unique once verification starts.
- Activation requires an exact policy schema. Missing or renamed gates and unknown fields are rejected.

### Remaining risks

- Dynamic signer resolution is implemented, but its account-registration, KMS, database-failover, and key-rotation paths have not been operated under production load.
- Publisher compromise remains a material risk until independent sources, quorum, replay protection, and source-specific keys are deployed and drilled.
- Runner request-nonce state is process-local, so horizontal execution is blocked until replay state is durable or a single-replica lease is proven.
- Internal cross-tenant substitution tests and review passed, but an independent adversarial review has not occurred.
- There is no production incident history or verified recovery-time evidence.

## Reliability and observability

The coordinator contains crash-safe saga and account-scoped reconciliation primitives, but production reliability is unproven. Required exercises include websocket loss with REST reconstruction, ambiguous sends, crash after signing, database failover, RPC disagreement, chain reorg, signer key rotation, rate limiting, and pause at every saga phase.

Every deployment must expose disabled, live, and ready health separately. A process being alive is not evidence that it may accept capital or entries.

## Verification completed in this branch

- Contract unit, fuzz, invariant, deployment, isolation, owner-consent, sequencer-quorum, turnover, and recovery suites.
- Rust unit and PostgreSQL migration/integration tests for account-scoped execution, fresh readiness, append-only evidence, and tenant uniqueness.
- Go provisioner and signer unit, race, vet, build, replay, mismatch, and rotation tests.
- Web unit, type, build, and onboarding lifecycle checks.
- Human-flow review of create, venue linkage, deployment, readiness, lifecycle commands, and withdrawal. The flow did not pass: it dead-ends at live funding, and Lighter account creation, rejected-link recovery, and complete withdrawal recovery are not usable end to end.
- Exact activation-policy, release-manifest, Render blueprint, identity, and leak checks.

These tests validate disabled software behavior. Browser launch and localhost binding were denied by the execution sandbox, so no claim is made that a real browser completed the flow. They do not satisfy audits, venue approval, oracle review, historical capture, shadow operation, or live exit proof.

## Release decision

**No-go for customer or operator capital today.**

The next permitted milestone is a disabled deployment of migrations, services, registry, quorum feed, and factory candidate. After independent audit and external gates pass, the existing singleton may run a $25-per-leg operator canary only if the current capital policy is satisfied or the narrowly scoped engineering-canary amendment is signed. Public onboarding remains blocked until the personal canary and cohort gates complete.
