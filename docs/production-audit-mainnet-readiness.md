# Production audit: mainnet readiness

## Executive summary

The repository is not ready to custody or trade mainnet capital. The contract suite is a sound
starting point for a dormant single-owner boundary, but a generic call path is not a safe venue
adapter and the research programme has not accumulated the evidence required to claim a durable
edge. This audit hardens the deployable core and records the gates that engineering cannot satisfy
by assertion.

## Critical issues (P0 — block release)

- [x] **A generic router selector could have been allowlisted.** Its declared notional is not a
  binding limit on actual asset outflow. The mainnet deployment profile now rejects a router input,
  starts halted, and deploys no allowed target. A typed, venue-specific adapter remains required.
- [x] **Deployment wiring had no independent post-deploy assertion.** `VerifyDeployment.s.sol`
  validates code presence, role separation, immutable references, limits, anchor wiring, and the
  halted state against explicit operator inputs.
- [ ] **No typed execution adapters exist.** A live adapter must bind asset, route, recipient,
  maximum input, minimum output, deadline, slippage, and reconciliation semantics. It also needs a
  verified venue specification and an independent review.
- [ ] **No real testnet order lifecycle exists.** The current proof is synthetic and has no
  execution venue. Successful order, cancel, partial fill, rejected hedge, and unwind evidence is
  mandatory before enabling an adapter.
- [ ] **The research gate is incomplete.** There is no 180-day immutable capture set or 60-day
  continuous paired-shadow record. This is elapsed evidence, not an implementation task.
- [ ] **No independent smart-contract audit has been completed.** The tracked audit scope is ready
  for handoff, but internal tests and reviews do not satisfy this requirement.

## High priority (P1 — fix before capital)

- [ ] Deploy the private collector with worker-only R2 credentials and private Postgres access;
  retain source-health and archive-write evidence.
- [ ] Implement verified executable spot quotes, authenticated perp lifecycle handling, and paired
  reconciliation. Do not fabricate fills when either venue is stale.
- [ ] Add portfolio covariance, factor, margin, liquidation, and emergency-unwind controls to the
  execution promotion path.
- [ ] Complete an operational key review: isolated owner and agent keys, recovery procedure,
  monitoring, alert routing, and owner-approved canary workflow.

## Security assessment

The owner is a deliberate human-control boundary and can defund or change policy. This is suitable
only for a single-owner system with disciplined key management; it is not decentralized custody.
The deployable core now fails closed: no venue is allowed and the guard begins halted. The generic
`execute` surface remains unsuitable for live use until it is replaced or constrained by typed
adapters. Public documentation must not receive runtime, wallet, venue, or strategy credentials.

## Observability assessment

The runtime stores raw events and source health, but production evidence does not yet exist because
the worker has not been provisioned with R2 credentials. Before capital, alert on archive failures,
feed staleness, nonce gaps, database errors, hedge state, margin state, and every halt transition.

## Test coverage gaps

- Venue-specific quote, approval, submission, cancellation, partial-fill, and unwind lifecycle.
- Typed intent bounds against adversarial calldata and upgraded venue contracts.
- Reorg recovery, sequencer outage, stale source, slippage spike, and margin-deterioration drills.
- Long-running archive integrity and shadow-fill reconciliation under duplicate events.

## Release decision

**No capital deployment.** A dormant, halted core may be verified only after its independent audit
and operator review. Live execution remains blocked until every unchecked P0 and P1 item has
evidence attached and the research gates in `docs/venue-gates.md` are satisfied.
