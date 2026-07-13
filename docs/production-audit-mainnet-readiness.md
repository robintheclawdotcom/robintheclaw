# Production audit: mainnet activation readiness

## Executive summary

Robin's typed production contract layer is live and source-verified on Robinhood Chain mainnet. It
launched halted and unfunded under a canonical 2-of-3 Safe and 48-hour timelock, with no agent,
market, route, or sequencer source installed. This completes the onchain deployment milestone and
creates a controlled base for staged activation.

Capital activation is a separate promotion decision. Authenticated venue streams, canonical chain
observations, executable quote authority, margin verification, operational infrastructure,
independent reviews, and elapsed research evidence must close the full control loop before the
deployment receives capital or execution authority.

## Mainnet deployment milestone

- [x] Deploy the typed, non-upgradeable v1 contract graph halted and unfunded.
- [x] Configure the Safe as timelock proposer, canceller, and executor without direct admin power.
- [x] Set the timelock as config authority and the Safe as treasury and immediate revocation authority.
- [x] Start with a zero agent, zero balances, no markets, no routes, and a fail-closed sequencer gate.
- [x] Re-read all contracts after deployment and verify provenance, roles, code hashes, limits, and state.
- [x] Source-verify the factory, gate, risk manager, adapter, vault, anchor, and timelock on Blockscout.
- [x] Confirm the deployment batch commitment was finalized on Ethereum.
- [ ] Rotate the Safe from bootstrap custody to device-separated operational owners.

See [mainnet-deployment.md](mainnet-deployment.md) for addresses, code hashes, review evidence, and
the canonical activation state.

## Critical issues (P0 - block capital activation)

- [ ] **The empirical promotion window is incomplete.** Capital activation requires 180 calendar
  days of verified capture and 60 continuous days of auditable shadow operation. These clocks cannot
  be shortened or backfilled.
- [ ] **Independent review is incomplete.** The contracts require an external audit, and the
  coordinator, both signer services, key topology, and recovery runbooks require an independent
  executor and operational-key review with no open critical or high findings.
- [ ] **Legal and venue approval is absent.** Written approval must cover the operating jurisdiction,
  automated trading, Stock Tokens, Lighter access, custody, and the canary operating model.
- [ ] **Production reconciliation inputs are unwired.** The coordinator cannot safely trade until an
  authenticated Lighter account stream and independently reconciled Ethereum-final Robinhood Chain
  event source continuously supply its append-only venue-event ledger.
- [ ] **Executable exit authority is unwired.** No production publisher currently supplies the fresh
  Lighter mark and block-pinned, reviewed Uniswap v4 exact-input quote required before every unwind
  send.

## High priority (P1 - complete activation infrastructure)

- [x] Replace caller-selected target and calldata execution with typed spot intents, a typed risk
  manager, and an internally constructed reviewed route.
- [x] Persist saga actions, native event identities, nonce reservations, send authorization, exact
  Robinhood requests, replacement-family outcomes, and operator recovery in PostgreSQL.
- [x] Keep automatic unwind attempts bounded while allowing an authenticated operator to allocate a
  new globally unique reduce-only order identity without reuse.
- [x] Preserve the exposure lock after failed-safe actions and provide reconciliation-only recovery
  from durable post-broadcast evidence.
- [ ] Verify Lighter collateral, margin, open position, funding, account identity, and subaccount
  isolation immediately before signing and during reconciliation.
- [ ] Apply production migrations through a dedicated release identity with rollback evidence,
  least-privilege roles, PgBouncer behavior tests, HA failover evidence, and a completed restore
  drill.
- [ ] Provision bucket-scoped R2 credentials, retention locks, daily manifest reconciliation,
  telemetry export, on-call routes, and tested incident automation.
- [ ] Complete the one-hour 2x-peak benchmark, 24-hour connector chaos test, 72-hour production soak,
  deterministic replay proof, and 180-day capacity projection.
- [ ] Meet contract branch-coverage, invariant, fuzz, mainnet-fork, formal-verification, reproducible
  bytecode, and provenance gates before external audit handoff.

## Security assessment

The intended authority split is sound but not operationally proven. The coordinator holds no private
key; the Lighter signer is isolated from EVM authority; and the Robinhood writer uses one
non-exportable KMS key. Separate HMAC scopes isolate intent admission, exit and recovery, venue
events, market authority, and signer calls. The services enter Render disabled and the database mode
enters `HALTED`.

The personal-vault generic call path used by the no-code test environment is constructor-locked to
Robinhood testnet chain ID 46630 and is absent from the mainnet deployment script. Mainnet custody
uses only the typed v1 vault, risk manager, and internally constructed spot adapter. The testnet
path must not be promoted, bridged, or redeployed as a mainnet substitute.

The remaining risk is at the dependency boundary. A compromised authenticated collector, quote
publisher, RPC pair, KMS policy, or Lighter subaccount could still feed or execute unsafe state if its
production identity and monitoring are not independently verified. No enable flag, successful build,
or dormant contract deployment substitutes for that review.

## Performance assessment

The action queue uses bounded leases, skip-locked claims, append-only evidence, and indexed recovery
lookups. That design is suitable for the intended event-driven rate, but there is no measured peak,
contention profile, database growth curve, R2 archival backlog profile, or failover benchmark. The
system therefore has no defensible production capacity claim.

## Observability assessment

The code records control versions, incidents, action events, native venue identity, sequence gaps,
and reconciliation state. Production metrics, traces, log sinks, alert routes, dashboards, and tested
dead-man responses remain unprovisioned. Required alerts include source gaps, archive backlog,
database failure, finality disagreement, signer or nonce divergence, stale cancellation, unhedged
exposure, margin deterioration, code-hash drift, storage pressure, and missing heartbeats.

## Recommended architecture changes

1. Make the runtime collector the only producer of normalized authoritative venue and chain events.
2. Deploy the execution-authority publisher as a separate read-only service with reviewed pool,
   hook, block, and runtime-code evidence.
3. Add authenticated Lighter account-state and collateral snapshots to the coordinator pre-send gate.
4. Move migrations and recovery drills into a dedicated release workflow with immutable evidence.
5. Keep unauthenticated public output documentation-only and delayed. Isolate the authenticated
   product API from the separate private, read-only operator surface; neither receives signing
   authority.

## Test coverage gaps

- Replacement-family winner and loser confirmation across reorg and dual-RPC disagreement.
- Exact-request replay after timeout, 409 conflict, process death, and signer restart.
- Lighter partial fill, terminal rejection, nonce gap, cancelled order, and exhausted automatic
  unwind followed by operator recovery.
- Poisoned payload before and after send authorization, lease theft, stale worker completion, and
  duplicate recovery requests.
- Zero-spot short recovery, mismatched spot balance deltas, malicious token behavior, multiplier
  transition, oracle pause, sequencer outage, and emergency close.
- PostgreSQL failover, point-in-time recovery, R2 upload and acknowledgement crash boundaries, and
  full deterministic replay under production-sized data.

## Action plan

1. Finish and independently review the authenticated Lighter and Robinhood event producers.
2. Build and verify the executable quote-authority publisher and live account-risk gate.
3. Complete the release, telemetry, retention, backup, restore, and incident infrastructure.
4. Close contract verification and independent audit findings.
5. Run the benchmark, chaos, soak, replay, and recovery evidence programme.
6. Complete the Safe owner rotation, operational key review, and independent audit against the
   deployed source-verified contract graph.
7. Accumulate the mandatory capture and shadow windows and pass statistical promotion.
8. Obtain legal, venue, and capital-activation approval before funding or authorizing a canary.

## Release decision

**Mainnet contract deployment is complete.** The release establishes the production governance and
custody boundary but does not activate trading capital. Funding and canary execution require a
separate promotion after every empirical, legal, venue, audit, key, and operational gate passes.
