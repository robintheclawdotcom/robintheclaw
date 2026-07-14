# Mainnet live-execution readiness

## Release decision

The capped `basis-aapl-v1` mainnet canary and live-execution services are enabled in code and in the
production Blueprint. The repository's internal audit of the exact release is the audit gate.

This decision enables the live lane; it does not bypass technical account readiness. The control
plane admits an account only while its canonical binding, funding, signer gas, authenticated venue
state, executable quotes, margin, oracle and sequencer health, nonces, reconciliation, and kill
switches are current.

## Released system

The live path includes:

- one isolated, non-upgradeable vault, risk manager, adapter, anchor, and KMS execution key per user;
- a registry binding execution accounts to approved factory versions, agents, and canonical graphs;
- account-scoped Lighter and Robinhood signers that resolve credentials and contract bindings privately;
- authenticated Lighter account-state and Robinhood dual-RPC finality publishers;
- an executable quote authority and keyless runner for the fixed `basis-aapl-v1` strategy;
- durable paired-entry, repair, unwind, reconciliation, and account-control state;
- owner launch, pause, resume, close, and flat-only withdrawal commands;
- global, strategy, account, guardian, and owner restriction paths.

Users pay deployment gas directly or through an ordinary funded relayer. Execution signers maintain
their own gas balances. Paymaster sponsorship is optional and is not part of launch readiness.

## Internal audit gate

The exact release is eligible only when repository checks pass and no internal critical or high
finding remains open across contracts, execution, signing, custody binding, keys, reconciliation,
and operational recovery. Remediated findings are retested against the release commit and recorded
with their evidence.

The internal review covers cross-user isolation, unauthorized withdrawal, deterministic deployment,
owner revocation, halt precedence, oracle and sequencer failure, signer substitution, nonce reuse,
partial and ambiguous fills, stream gaps, dual-RPC disagreement, reorgs, crash recovery, pause during
each saga phase, and flat-only close and withdrawal.

## Runtime admission

For every entry, the control plane requires:

1. the approved strategy and risk-policy digests for the execution account;
2. canonical account, signer, Lighter subaccount, vault, adapter, risk manager, and factory bindings;
3. verified user funding, Lighter collateral, owner gas where needed, and execution-signer gas;
4. authenticated venue state and executable quotes less than five seconds old;
5. at least twice maintenance-margin coverage and no unknown orders or positions;
6. aligned EVM and Lighter nonces with no unresolved submission ambiguity;
7. healthy route, oracle, and sequencer quorum;
8. `ACTIVE` global, strategy, and account controls.

Any cross-account signature, nonce reuse, unapproved send, unresolved ambiguity, or shared integrity
failure stops new admission. Reduce-only recovery remains available. An account-specific failure
restricts that tenant unless the evidence indicates a shared failure.

## Current product state

The product exposes the complete agent lifecycle: provision an execution account, associate a
user-owned Lighter subaccount, deploy and authorize the per-user Robinhood graph, verify funding and
gas, launch, pause, resume, close, and withdraw after both venues reconcile flat. The UI reports each
technical readiness item directly and does not depend on a sponsorship policy.

The historical singleton deployment remains an operator-only canary artifact. Customer capital uses
only isolated per-user graphs created by the approved factory release.
