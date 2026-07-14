# Mainnet live-execution internal audit

Date: 2026-07-14

Release: `basis-aapl-v1` engineering canary

Scope: onboarding, per-account custody, signing, market data, execution, repair, reconciliation, operations, and owner recovery

## Decision

The repository release is approved for one mainnet engineering-canary account with the following immutable limits:

- one account;
- $25 maximum notional per leg;
- $50 maximum gross exposure;
- 1x maximum exposure;
- one active episode;
- $50 maximum daily turnover;
- AAPL/USDG on Robinhood Chain paired with the matching AAPL perpetual on Lighter.

The production policy and every live service flag are enabled. The promotion record is append-only and binds this audit's SHA-256 digest. Account launch still requires the runtime to prove current funding, finality, nonce alignment, executable quotes, oracle health, sequencer health, reconciliation, and exact account bindings. Those checks are execution invariants, not release switches.

This review found no unresolved code-fixable P0. It found and fixed multiple P1 defects before approval. No funded mainnet order or vault transaction was sent during this audit, so the audit does not claim a completed live fill.

## Approved architecture

- `PairIntent v2` binds the execution account, agent, source evaluation, strategy manifest, risk version, Lighter account and key indexes, Robinhood vault, and Robinhood signer. Entry and unwind identifiers are deterministic and domain-separated.
- Execution controls, snapshots, events, nonces, turnover, incidents, and episodes are account-scoped. Shared-integrity failures can still restrict global admission.
- Lighter and Robinhood signing requests contain an execution-account identifier. Each signer resolves its own private binding and rejects account, key, vault, signer, policy, code-hash, nonce, and returned-response mismatches.
- Lighter API credentials are generated inside the private provisioner, encrypted with account-bound authenticated data, and never stored by the product API.
- Each Robinhood account receives a non-exportable KMS execution key and an owner-specific non-upgradeable vault, risk manager, adapter, and registry binding.
- The owner retains withdrawal, revocation, halt, and terminal-recovery authority. The agent, relayer, guardian, and governance paths cannot withdraw owner funds.
- Gas sponsorship is absent from the mainnet signing path. The owner pays deployment and owner-action gas in ETH; the isolated execution signer has a separately monitored ETH balance.
- Authenticated publishers reconstruct Lighter state and Robinhood dual-RPC finality. Readiness expires after five seconds and fails closed on gaps, ambiguity, unknown orders or positions, stale quotes, insufficient margin, or mismatched identities.
- The strategy runner is keyless. It accepts only the pinned `basis-aapl-v1` manifest and turns approved, fresh evaluations into `PairIntent v2`.
- Execution remains perp-first IOC. The terminal perp fill determines the spot size. Spot failure schedules bounded reduce-only repair. Unresolved send ambiguity restricts the account and global new-entry admission until reconciliation.
- Pause immediately stops entries and reduces to flat. Resume recomputes all readiness evidence. Close reduces, revokes the agent, and reconciles. Withdrawal is prepared only after both venues are flat and remains an owner-signed action.

## P1 findings fixed

### Quote and oracle integrity

- Rejected Chainlink rounds that cannot be represented as the contract's `uint80` round identifier instead of truncating them.
- Prevented quote-age laundering by carrying the authoritative publisher time through executable quote admission.
- Bound unwind quotes to the expected UI multiplier and minimum oracle round.
- Corrected the AAPL spot/perpetual unit conversion and pinned the route, token decimals, multiplier, pool fee, tick spacing, hooks, and pool identity.
- Required the AAPL source-feed runtime hash as a fail-closed deployment value. No value was guessed when it could not be retrieved from two independent RPC observations in this environment.

### Risk and custody

- Closed the partial-exit turnover loophole: every entry and exit leg consumes the versioned daily-turnover allowance.
- Strengthened deployment checks for timelock proposer, canceller, and executor authority, including rejection of open execution.
- Enforced immediate owner revocation, halt precedence, exact approval cleanup, deterministic deployment, cross-user isolation, and factory/registry code-hash checks.

### Signing and tenant isolation

- Split Lighter market metadata from Robinhood RPC configuration so signer and provisioner services cannot receive unrelated chain credentials.
- Fixed first-use Robinhood signer journal initialization. A missing journal is accepted only when two RPC observations agree that the canonical signer nonce is exactly zero.
- Added durable exact nonce-and-request-digest claims for Lighter signing. Exact replay is idempotent; substitutions and conflicting claims are rejected.
- Removed account, key, vault, signer, code hash, and response identity material from caller authority. Signers resolve canonical values privately.

### Execution and recovery

- Replaced global halts for deterministic tenant failures with account restriction and incidents, preserving other tenants.
- Made shared ambiguity and overdue reconciliation restrict both the affected account and global entry admission once, without repeated version churn.
- Required spot fill size and receipt identity to match the terminal perp-derived hedge before an episode can become hedged.
- Added durable natural-exit binding, exact open-episode resolution, crash recovery after signing, REST reconstruction after stream gaps, and idempotent command and intent status queries.

### Product and deployment

- Replaced the paper-only self-service creation path with a server-controlled live lifecycle from setup through closed, plus blocked recovery through reconciliation.
- Added public APIs for execution-account creation, Lighter association, Robinhood graph preparation and confirmation, readiness, durable commands, and command status.
- Fixed the owner-gas UX: it now checks Robinhood Chain ETH and never reports sponsorship as a substitute for owner gas.
- Added ordered, checksum-verified production migrations and Render pre-deploy commands. Render's native deploy environment includes the PostgreSQL client used by these commands.
- Enabled the coordinator, both publishers, quote authority, strategy runner, live evaluation, scheduler, exit publisher, both provisioners, both signers, and three oracle/sequencer publishers in the production Blueprint.

## Verification performed

- Contracts: 110 Foundry tests passed, including unit, fuzz, and invariant coverage for isolation, withdrawal authority, deterministic deployment, halt precedence, revocation, oracle and sequencer failure, malicious route inputs, approval cleanup, recovery, and reproducible deployment checks.
- Execution and custody services: targeted Rust and Go suites covered cross-account substitutions, independent nonces, partial fills, ambiguous sends, stale state, quote and multiplier changes, replay, first-use signer state, pause/close recovery, and account-versus-global restriction behavior.
- Go services passed compile, `go vet`, targeted race suites, and package tests that do not require opening a local listener.
- Rust workspaces passed formatting, compilation, clippy, and unit tests. PostgreSQL integration cases were compiled and reviewed; a local PostgreSQL server was not available in the sandbox.
- Product and web suites passed unit tests, type checking, and production builds. Playwright test discovery passed. The repository does not define a separate web lint command. The sandbox denied local port binding, so a real browser could not complete the mocked flow here.
- Blueprint, live-policy, migration-manifest, operations-contract, promotion-ledger, strategy-manifest, live-execution protocol, leak, and identity validators passed or are included in the release validator.
- Internal key review approved account-bound encryption, non-exportable KMS use, signer-private resolution, service-secret separation, and rotation/revocation paths at source and configuration level.
- Internal recovery approval covers deterministic database migrations, stream reconstruction, crash replay, signer replay, command resumption, owner revocation, and tested contract recovery paths. It does not claim a restore of a deployed production database or KMS estate.

## Deployment inputs still required

The enabled release will refuse to start or launch an account until operators provide and verify these real values:

- exact Lighter AAPL market index, base decimals, and price decimals from Lighter's authenticated official API;
- the observed AAPL source-feed runtime hash, identical through two independent Arbitrum RPCs;
- deployed factory, registry, timelock, quorum-feed, vault graph, signer, token, router, Permit2, pool, and policy addresses and runtime hashes;
- private database URLs, KMS key identifiers, envelope-encryption keys, HMAC keys, and independent RPC endpoints;
- owner ETH, execution-signer ETH, Robinhood vault USDG, and Lighter subaccount USDC;
- the owner-signed Lighter key association and Robinhood agent authorization;
- current 2-of-3 oracle and sequencer quorum, executable entry and exit liquidity, fresh authenticated venue state, and flat reconciliation before initial activation.

These are external state and secret values, not disabled product behavior. Missing or inconsistent values stop the affected startup or account launch instead of producing an unapproved transaction.

## Residual risks accepted for the bounded canary

- A complete exit depends on fresh oracle and sequencer observations plus available route liquidity. Owner terminal recovery can return raw AAPL if normal conversion is unavailable.
- USDG and the Chainlink proxy can change behavior behind their proxy addresses. Runtime code-hash checks detect proxy bytecode changes but do not freeze an implementation controlled outside this repository.
- Access-control roles are not enumerable on chain. Deployment receipts and logs must be retained to prove there are no undisclosed role holders.
- Distinct RPC hostnames do not alone prove distinct infrastructure ownership. Operators must configure independent providers.
- A signer crash after a durable claim can intentionally block that credential until reconciliation. This prefers duplicate-trade prevention over automatic liveness.
- Previously initialized databases without the new checksum ledger need a one-time inspected baseline. The migration scripts refuse to guess historical state.
- The audit validates the release and bounded recovery paths. The first funded entry, exit, pause, close, and withdrawal remain operational evidence to capture during the canary.

## Approval record

Approval identity: `internal-release-audit-2026-07-14`

Approved scope: one account, `basis-aapl-v1`, limits stated above

Promotion: `registered -> canary_eligible`

Production policy: enabled

Capital policy: enabled

Any cross-account signature, nonce reuse, unapproved send, unresolved ambiguity, code-hash mismatch, or critical/high incident requires immediate restriction and a new audit digest before reactivation.
