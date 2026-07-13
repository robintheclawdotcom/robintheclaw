# Mainnet execution operations

## Operating state

These artifacts define the observability and drill contract for a disabled mainnet execution
foundation. They do not show that a metric producer, Prometheus, Grafana, paging receiver, or
incident controller is deployed. They are not authorization to enable a service or place capital.

No execution flag may be enabled from this runbook. Keep all accounts unfunded and all controls
`HALTED` until the repository's activation policy, independent reviews, and account readiness gates
pass. An empty dashboard is missing evidence, not a healthy system.

The versioned metric contract is
[`ops/mainnet-live/metrics/contract.v1.json`](../ops/mainnet-live/metrics/contract.v1.json).
Producers must expose those exact names and semantics before their readiness result can pass. Raw
user identifiers, wallet addresses, order payloads, signatures, keys, and strategy inputs do not
belong in labels. `execution_account_id` must remain opaque.

## Alert contract

The v1 rules use five seconds as the maximum authoritative state age, two times maintenance margin
as the minimum entry coverage, and zero tolerance for stream gaps, unknown venue state, nonce drift,
RPC disagreement, unresolved sends, and cross-account boundary incidents. Gas runway is an
operational paging signal; the versioned readiness policy remains authoritative.

Every `high` or `critical` alert has `stage_reset: "true"`. The incident controller must turn a
firing alert into an append-only incident record, set the affected rollout stage's clean-observation
start to unset, and require reconciliation before a new observation period begins. Alert recovery
alone cannot close an incident or restart the clock.

### Unhedged exposure

Set the account and strategy controls to `REDUCE_ONLY`. If bounded repair cannot be proved safe from
fresh venue state, set the account to `HALTED` and block global new entries. Preserve the intent,
signed requests, venue acknowledgements, fills, receipts, nonce evidence, and repair decisions.
Close only after both venues are authoritative and the account is reconciled hedged or flat.

### Source age or stream gap

Block admission for the affected account immediately. Reconstruct from an authenticated REST
boundary, reconcile sequence numbers and all positions, orders, collateral, fills, and nonces, then
publish a new complete snapshot. Do not clear the incident from a websocket reconnect alone.

### Margin coverage

Block entries and evaluate a reduce-only unwind from fresh authenticated state. Do not assume a
deposit is final until the account publisher proves it. Resume only after coverage is at least two
times maintenance margin and every other readiness check passes.

### Unknown orders or positions

Set the account to `HALTED` and block global admission. Reconstruct the account from the venue,
associate every object with an admitted intent or classify it as an incident, and reconcile nonces.
Unknown venue state is never repaired by deleting local records.

### Nonce drift

Stop signing for the account. Compare reserved, signed, broadcast, accepted, and authoritative
venue nonces. Preserve crash and failover evidence. Rotate a key only after pending work is resolved;
rotation cannot be used to skip an unexplained nonce.

### Signer or KMS failure

Block the affected account. A binding mismatch, unknown key version, incorrect public address, or
invalid returned response is a security incident, not a transient retry. Preserve request and
response digests, key references, KMS audit identifiers, and signer journal records without
recording credentials or signatures in telemetry.

### Finality disagreement or reorg

Set global admission to `HALTED`. Query the two independent RPC sources and the finalized ancestry;
do not select the more favorable response. Reconcile every affected receipt and account state. A
new matching response is insufficient until the disputed history has a recorded resolution.

### Command or outbox lag

Block new launch and resume commands. Determine whether delivery was absent, accepted, or ambiguous
before retrying. Query downstream command status by the durable command identifier. Never replay a
capital-bearing command solely because a client or worker timed out.

### Gas readiness

Block launch and resume for the affected account. The user may fund deployment gas and the operator
may fund the non-exportable execution signer through their separate procedures. Sponsorship is not
a prerequisite and its failure does not change custody. Verify finalized balances and the policy's
gas-ready result before proceeding.

### Control mode

Confirm the restrictive mode is intentional and reflected in the coordinator and, where applicable,
the onchain registry. A restrictive mode may remain active without being an incident. Only the
authorized timelocked path may loosen an onchain control; alert recovery, restart, or configuration
change cannot do so.

### Unresolved ambiguity

Set the account and global admission to `HALTED`. Do not resend. Reconstruct acceptance, order,
receipt, fills, positions, balances, and nonce state from authoritative sources. The incident closes
only when one outcome is proved and the resulting account state reconciles.

### Cross-account incident

Set global admission to `HALTED` and reject signing. Preserve both account bindings, request and
response digests, signer journals, nonce reservations, canonical vault graph, key versions, and
venue events. Check every account touched by the component. No affected credential or execution
key returns to service without independent review.

### Stage reset

Record the affected stage, alert, severity, opening time, account or shared scope, release digest,
and evidence references. Set the clean-observation start to unset. After the root cause is resolved,
repeat reconciliation and applicable kill-path drills, close the incident with review attribution,
and start a new full observation period. Time before the incident does not carry forward.

### Telemetry absence

Keep global admission `HALTED`. Verify producer health, scrape discovery, contract version, rule
evaluation, dashboard data source, paging delivery, and incident persistence. Absence is resolved
only after an end-to-end synthetic high-severity signal creates and then closes a non-capital
incident with retained evidence.

## Kill-path drills

Run all five drills against the exact release candidate before capital. Use unfunded accounts,
forked state, or venue sandboxes until the activation policy separately authorizes capital. A drill
must exercise the real authentication, control, signer-rejection, persistence, publishing, and
reconciliation paths; changing a database row or dashboard value by hand is not a drill.

For every drill, retain:

- release commit, immutable image and strategy-manifest digests, environment, and UTC start/end;
- initiator role and opaque request/audit identifiers, without identity or credential material;
- control events before and after, durable commands, signer rejections, and relevant transaction or
  venue identifiers;
- authoritative snapshots for orders, positions, collateral, nonces, receipts, and finality;
- alert, page-delivery, incident, stage-reset, and reconciliation records;
- independent reviewer decision and evidence digest.

Successful evidence updates `robin_kill_path_last_success_timestamp_seconds`. The metric must not be
advanced by a scheduled job or a partial drill.

### Global kill path

1. Begin with the release candidate healthy but globally `HALTED`; prove an entry is rejected.
2. In a non-capital environment only, authorize `ACTIVE` through the real control path and admit two
   independent dry-run accounts.
3. Invoke the global restrictive authority while each saga is paused at a different phase.
4. Verify no new intent is admitted, both accounts move through bounded reduction, and the system
   reaches authoritative flat state or remains fail-closed on ambiguity.
5. Restart and fail over the coordinator; verify the global restriction and durable work survive.

Expected evidence: global control transition, two account-scoped saga journals, entry rejection,
reduction or ambiguity outcome, dual-venue flat snapshots, restart/failover continuity, page, and
incident record. Restoring `ACTIVE` is a separate authorized action and is not part of the drill.

### Strategy kill path

1. Admit dry-run work for `basis-aapl-v1` and a control fixture that cannot submit live orders.
2. Restrict `basis-aapl-v1` at the strategy scope.
3. Verify its accounts reject entry and reduce, while the control fixture demonstrates that the
   global control did not change.
4. Restart the coordinator and verify strategy restriction precedence remains intact.

Expected evidence: strategy-version binding and manifest digest, strategy control transition,
targeted rejection and reduction, unchanged global control, durable state after restart, flat
snapshots, and reconciliation decision.

### Account kill path

1. Prepare two isolated dry-run accounts with independently reserved EVM and Lighter nonces.
2. Issue `pause` for one account through the user command path during each saga phase in turn.
3. Verify that account rejects entry, performs bounded reduction, reconciles flat, and preserves its
   restrictive control through restart.
4. Verify the second account's credentials, vault graph, keys, nonces, events, and control do not
   change.

Expected evidence: authenticated idempotent command, outbox and coordinator status, account control
transition, per-phase saga evidence, signer/account binding, independent nonce streams, flat
snapshots, and a negative cross-account comparison.

### Guardian kill path

1. Against the exact unfunded deployment, have the guardian restrict the registry from `ACTIVE` to
   `REDUCE_ONLY`, then to `HALTED`.
2. Verify the guardian cannot activate a factory, replace an agent, loosen a mode, or withdraw.
3. Confirm both RPC sources agree on the finalized events and resulting registry state.
4. Verify coordinator admission remains blocked when its local view is stale or less restrictive.

Expected evidence: transaction and calldata digests, finalized receipts from both RPC sources,
registry events and state, negative authorization calls, admission rejection, and code-hash and
role bindings. Loosening requires the timelocked governance drill and is outside guardian authority.

### User kill path

1. Through the authenticated command path, pause and close an unfunded user account while each saga
   phase is simulated.
2. Verify close revokes the agent and the KMS-backed signer rejects subsequent execution requests.
3. Verify `withdraw` remains unavailable until both venues report known, flat state.
4. Using the owner wallet manually, exercise the prepared withdrawal or terminal-recovery action and
   verify no guardian, relayer, agent, or governance identity can direct funds elsewhere.

Expected evidence: user command and wallet signatures, owner and treasury binding, agent-revocation
event, signer rejection, dual-venue flat snapshots, unsigned owner action, finalized receipt,
balance reconciliation, and negative withdrawal-authority calls.

## Instrumentation acceptance

Before monitoring can be a launch gate:

1. every producer exposes the exact v1 metrics and labels from the contract;
2. duplicate, missing, stale, restarted, and counter-reset behavior is tested;
3. Prometheus loads the rules without error and retains enough history for the rollout stage;
4. every alert is injected end to end through paging and append-only incident storage;
5. dashboard panels are compared with primary venue, chain, signer, KMS, database, and command
   evidence for two isolated accounts;
6. high and critical injections reset the stage clock, while warnings do not;
7. label cardinality and redaction are reviewed under representative account load.

Until those checks are retained for the release candidate, the dashboard is a specification, not
production evidence.
