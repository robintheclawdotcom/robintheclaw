# Mainnet live-execution internal audit

Date: 2026-07-18

Release: `basis-aapl-v1` engineering canary

Scope: onboarding, per-account custody, signing, market data, execution, repair, reconciliation, operations, owner recovery, deployment tooling, and release controls

## Verdict

The static release artifact is approved for one operator-controlled mainnet engineering-canary account with these immutable limits:

- one account;
- $25 maximum notional per leg;
- $50 maximum gross exposure;
- 1x maximum exposure;
- one active episode;
- $50 maximum daily turnover;
- AAPL/USDG on Robinhood Chain paired with the matching AAPL perpetual on Lighter.

The repository policy permits execution and capital activation. There is no legal, third-party-audit, gas-sponsorship, observation-period, or shadow-period switch in the engineering-canary gate. Internal review is the audit authority for this release.

This approval does not assert that production is deployed or ready. At audit time, most live Render services did not exist, the available signer and provisioner shells were suspended, the new AWS OIDC/KMS topology was not provisioned, the personal factory and registry were not deployed, no account had current readiness evidence, and no funded mainnet round trip had occurred. The deployed singleton remained halted and unfunded. Those are missing external facts, not disabled release flags. The runtime must remain `HALTED` until it proves them.

No confirmed code-level P0 or P1 remained in the reviewed release. Static analysis produced findings discussed below; none established an exploitable cross-account authorization, withdrawal, or execution path. This is a bounded internal review, not a claim that the system is defect-free.

## Architecture approved

- `PairIntent v2` binds the execution account, agent, source evaluation, strategy and risk versions, Lighter account and key, and Robinhood vault and signer. Entry and unwind identifiers are deterministic and domain-separated.
- Execution accounts, controls, snapshots, events, nonces, turnover, incidents, and active episodes are account-scoped. Shared-integrity failures can still restrict global admission.
- The Lighter and Robinhood signers accept only an execution-account identifier from callers. They resolve canonical private bindings and reject account, key, vault, signer, code-hash, policy, nonce, journal, and returned-response mismatches.
- Every capital or lifecycle response in the coordinator, application, provisioner, signer, scheduler, strategy-runner, and quote-authority chain is authenticated over the route, caller, request nonce, status, and exact response body.
- Lighter credentials are generated inside the private provisioner, envelope-encrypted with account-bound authenticated data, and never returned to or stored by the product API. Terminal close requires canonical owner-signed Lighter key revocation.
- Each customer account receives a deterministic, non-upgradeable vault, risk manager, and adapter graph. The user wallet is immutable owner and treasury. Agent, guardian, relayer, and governance paths cannot withdraw user funds.
- Robinhood execution uses one non-exportable KMS key per account. Render services assume distinct least-privilege AWS roles through OIDC; static AWS credentials and caller-supplied key identities are rejected.
- Gas sponsorship is not part of mainnet onboarding or signing. The owner submits deployment, authorization, deposit, revocation, and withdrawal transactions directly through the connected wallet and pays ETH. The execution signer has an independently monitored ETH balance.
- Publisher database roles and row-level policies isolate each account, AAPL relay, and sequencer source. Shared legacy publisher roles are locked down. Product databases retain opaque bindings and public addresses, not signer credentials.
- Authenticated publishers reconstruct Lighter state and Robinhood dual-RPC finality. Entry evidence expires after five seconds and fails closed on gaps, ambiguity, unknown orders or positions, stale quotes, insufficient margin, unhealthy route dependencies, or identity mismatches.
- The keyless runner accepts only the pinned `basis-aapl-v1` manifest. Strategy code, market, route, calldata, leverage, thresholds, and repair policy are not user inputs.
- Execution remains perp-first IOC. The terminal perp fill determines spot size. Spot failure schedules bounded reduce-only repair. An unresolved send ambiguity restricts that account and global new-entry admission until reconciliation.
- Pause blocks entry and reduces to flat. Resume reruns every readiness check. Close reduces, revokes Robinhood and Lighter execution authority, and reconciles. Withdrawal is an owner-signed action available only after both venues are authoritatively flat.

## P1 findings closed during review

### Authentication and tenant isolation

- Added response authentication to all privileged service-to-service request chains. Missing, forged, replay-substituted, oversized, or cross-service responses fail before status or body interpretation.
- Removed account, key, vault, signer, code hash, and response identity from caller authority. Signers resolve canonical values privately.
- Added durable exact nonce-and-request-digest claims for Lighter signing. Exact replay is idempotent; conflicting claims are rejected.
- Fixed first-use Robinhood signer journal initialization. A missing journal is accepted only when independent RPC observations agree that the canonical signer nonce is zero.
- Added per-publisher roles, forced row-level security, source-bound journals, minimal grants, and owner-secret scrubbing for all sequencer and AAPL relay instances.

### Execution and reconciliation

- Bound quotes to authoritative source time, exact unit scaling, UI multiplier, oracle round, market identity, strategy manifest, and account.
- Required unwind completion to match the immutable exit request and authoritative saga, including account, intent, request, quote, digest, deadline, size, lifecycle reason, and accepted execution proof.
- Closed partial-exit turnover accounting and retained one active episode per account.
- Required spot receipt and fill size to match the terminal perp-derived hedge before an episode can become hedged.
- Made deterministic tenant failures account-local while shared ambiguity and overdue reconciliation restrict global admission.
- Added crash recovery after signing, REST reconstruction after stream gaps, exact nonce alignment, and idempotent command and intent status.

### Custody and owner control

- Enforced immediate owner revocation, halt precedence, exact allowance cleanup, deterministic deployment, cross-user isolation, and factory and registry code-hash checks.
- Required exact timelock proposer, canceller, and executor authority and rejected open execution.
- Kept all owner-value movement outside agent, relayer, guardian, and governance authority.

### Product, HTTP, and deployment

- Replaced the paper-only self-service path with the live lifecycle from setup through closed, plus blocked recovery through reconciliation.
- Added execution-account creation, Lighter link and revocation, Robinhood preparation and confirmation, readiness, execution status, durable commands, command recovery, and flat-only withdrawal APIs.
- Added explicit USDG, Lighter USDC, owner ETH, and execution-signer ETH readiness. No sponsorship credit or policy is consulted.
- Bounded request and response bodies, pinned same-origin enforcement to the configured public origin, refused redirects on signed requests, and applied explicit HTTP timeouts.
- Added ordered, checksum-verified migrations, exact release locks, database authority provisioning, and an idempotent Render bootstrap that stages services suspended, verifies exact commits, requires signed quiescence evidence, and rolls back to `HALTED`.
- Kept the public web service running during preparation and prevented the bootstrap from assuming that jobs can be created from suspended services.

## Verification

### Contracts

- Foundry: 110 passed, 0 failed, 1 intentionally skipped without a fork endpoint.
- Robinhood Chain mainnet fork: one exact $25 AAPL entry, complete exit, allowance cleanup, owner halt, and flat withdrawal passed against the pinned live router and tokens.
- Slither's unfiltered scan covered 51 contracts with 100 detectors and returned 92 results: 5 high, 27 medium, 35 low, and 25 informational. Four high results were `transferFrom` heuristics on owner/treasury/vault-bound flows; the fifth was an incorrect-exponentiation false positive inside the vendored OpenZeppelin `mulDiv` implementation. Exact source suppressions document the four reviewed flows, and CI excludes third-party dependencies while retaining fail-on-high. The release scan returned 62 first-party results: 0 high, 17 medium, 35 low, and 10 informational.
- Factory, registry, risk, vault, adapter, quorum-feed, malicious-token, malicious-router, revocation, recovery, isolation, and deterministic-bytecode cases are covered by unit, fuzz, invariant, and fork tests.

### Services and databases

- Six Rust workspaces passed formatting, locked compilation, clippy with warnings denied, and all non-database tests: 237 passed, 0 failed.
- Every ignored Rust PostgreSQL case was executed against PostgreSQL 18.4: application 7/7, coordinator 2/2, and runtime 1/1 passed.
- Twelve Go modules passed formatting, module verification, package tests, race tests, vet, and `govulncheck`: 72 module-level checks passed, 0 failed.
- Fifty-two ordered migrations across six ledgers applied on clean databases and replayed idempotently. Checksum mutation, partial-chain rollback, exact release lock, five invalid authority states, one canonical fresh-flat state, and per-role isolation all passed.
- Render bootstrap tests passed 32 runs and 287 assertions. Blueprint validation passed 15 runs and 49 assertions.
- Rust advisory scans found no known vulnerabilities. They reported a yanked transitive `spin` version and unmaintained transitive `derivative`, `paste`, and `proc-macro-error2` packages. Go analysis found no called or imported vulnerability; an unused `x/crypto/openpgp` package carried an advisory with no available fix.

### Product and browser

- Web unit tests: 37 passed, 0 failed. Type checking and the production build passed; all 13 static pages were generated.
- Chromium executed all 18 desktop and mobile Playwright scenarios. The full human-flow scenario created an account, associated Lighter, deployed and authorized the owner graph, represented both funding legs and gas, launched, showed a hedged episode, paused and unwound, resumed, closed, revoked both venue authorities, recovered from transaction failures and reloads, reconciled flat, and withdrew.
- The complete browser lifecycle uses controlled venue and chain fixtures; it proves UI behavior and state recovery, not a funded mainnet transaction.
- A real application API and fresh PostgreSQL run verified agent creation, execution-account creation, and fail-closed readiness. External preparation correctly stopped when the undeployed provisioners were unavailable.
- The public `/app` returned HTTP 200 over a valid TLS connection. Public reachability does not imply that the absent execution services are live.

### Security and release checks

- Semgrep ran 677 current community rules over 560 tracked files and returned 14 findings: one error-level command report and 13 warnings. Manual review found the command report and four related Ruby reports use argv-based process APIs without shell evaluation; eight response-writer reports emit JSON or escaped Prometheus text; and the dynamic regular expression receives only hardcoded field names. Confirmed CI supply-chain and dependency-cooldown findings from the initial scan were fixed.
- `npm audit --omit=dev --audit-level=high` reported zero vulnerabilities.
- Repository leak, identity, editorial-lock, migration, strategy-manifest, policy, promotion-ledger, operations-contract, AWS bootstrap, Blueprint, and static release validators passed.
- The aggregate release validator passed and explicitly reports that it validates static artifacts, not deployment, telemetry, funding, or account readiness.

## Current production state

The following was observed through read-only production inspection:

- the public web, API, research collector, and paper agent were running;
- the Lighter provisioner, Robinhood provisioner, and Robinhood signer existed as suspended shells;
- the execution coordinator, account publisher, quote authority, strategy runner, exit publisher, live control, Lighter signer, three sequencer publishers, and three AAPL relays did not exist;
- the singleton deployment record remained `halted-unfunded`, with no installed agent, zero settlement balance, no configured market, and an unbound sequencer source;
- no AWS credentials were available to provision the reviewed OIDC roles or KMS keys;
- no funded account, owner signatures, live telemetry, paging integration, signed quiescence receipt, or completed mainnet round trip was present.

The repository therefore cannot truthfully claim that any user can launch and trade today. The code and static policy are canary-eligible; the deployed system is not.

## Required live evidence

Activation requires real, current evidence for the selected account:

- the reviewed release commit deployed across every required service;
- AWS OIDC roles, envelope key, and per-account KMS key with exact least-privilege policies;
- deployed and verified personal factory, registry, timelock, quorum feeds, user graph, route, tokens, and runtime code hashes;
- completed governance operations and safe owner rotation;
- distinct database roles and pairwise HMAC secrets;
- owner ETH, signer ETH, vault USDG, and Lighter USDC;
- owner-signed Lighter association and Robinhood agent authorization;
- three independent oracle and sequencer publishers with fresh 2-of-3 quorum;
- executable entry and exit quotes, fresh authenticated venue state, finality agreement, aligned nonces, sufficient margin, and flat reconciliation;
- live emission and paging for the required metrics and a successful forced `REDUCE_ONLY` and `HALTED` drill;
- a signed quiescence receipt immediately before controlled activation.

The bootstrap must prepare and verify services suspended first. It must not activate an account or send capital merely because this audit and static policy pass.

## Residual risks accepted for the bounded canary

- The test suite declares 22 required operational metrics, but only a subset is emitted by the current account publisher and the sequencer publishers use a separate metric vocabulary. Live telemetry and paging must be reconciled before activation.
- The public web rate limiter is process-local. A multi-instance deployment needs shared enforcement at the edge or in a durable store.
- The content security policy still permits inline style or script behavior required by current dependencies. This increases impact if an independent injection flaw is introduced.
- The legacy testnet smart-wallet proxy can submit authenticated, unsponsored Alchemy wallet operations and consume provider quota. Mainnet owner actions bypass that proxy and use the connected wallet directly.
- Lighter request-authentication replay tracking is process-local. Durable transaction identity, business nonce, and journal claims prevent a distinct capital action, but durable request-nonce storage would add defense in depth.
- The bootstrap refuses a release upgrade while registered accounts exist because a reconciler for safely rotating active account bindings is not yet implemented.
- A complete exit depends on current oracle, sequencer, venue, and route availability. Owner terminal recovery can return raw AAPL if normal conversion is unavailable.
- USDG and AAPL remain controlled by upstream upgrade authorities. Finalized code-hash monitoring restricts new entry after a detected change but cannot prevent the upgrade itself.
- A signer crash after a durable claim can intentionally block that credential until reconciliation. This prefers duplicate-trade prevention over automatic liveness.
- No restore of a deployed production database or KMS estate, funded pause/close/withdrawal drill, or production paging drill has been observed.

## Approval record

Approval identity: `internal-release-audit-2026-07-17`

Approved scope: one account, `basis-aapl-v1`, limits stated above

Promotion: `registered -> canary_eligible`

Production policy: enabled

Capital policy: enabled

Legal review: outside repository scope and not a release gate

Third-party audit: not required

Any cross-account signature, nonce reuse, unapproved send, unresolved ambiguity, code-hash mismatch, or critical/high incident requires immediate restriction and a new audit digest before reactivation.
