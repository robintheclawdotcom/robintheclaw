# Internal audit scope: typed RWA custody boundary v1

## In scope

- `contracts/src/RwaStrategyVault.sol`
- `contracts/src/MandateRiskManagerV1.sol`
- `contracts/src/UniswapV4SpotAdapter.sol`
- `contracts/src/SequencerGate.sol`
- `contracts/src/RwaDeploymentFactory.sol`
- `contracts/src/AttestationAnchor.sol`
- `contracts/src/interfaces/`
- `contracts/script/Deploy.s.sol`
- `contracts/script/DeployGovernance.s.sol`
- `contracts/script/VerifyDeployment.s.sol`

The deployed source is commit `2a3aef84fda01c4f58088eafb04502cf8c27662d`, compiler `0.8.28`,
Shanghai EVM, optimizer enabled with 200 runs, and IR compilation. The deployment transaction and
runtime hashes are frozen in [`deployments/mainnet.json`](../deployments/mainnet.json).
`SyntheticProofVault`, `TestUSDG`, and `DeployTestnet.s.sol` are excluded because they cannot execute
trades and exist only for the synthetic testnet proof.

## Deployment profile

The deployed target is Robinhood Chain mainnet, chain ID 4663. The factory atomically created a
halted, unfunded vault, risk manager, zero-market adapter, and vault-owned attestation anchor. The
factory retains no authority. A one-time-bind sequencer gate starts unbound and reports down.
Administration is timelocked, emergency recovery pays a separate Safe, the guardian can only
restrict operation, and the agent can submit only typed spot intents. The initial agent is zero.

V1 permits long-spot entry and spot sale for reduction or exit. Short-spot entry is unsupported. The
off-chain perpetual leg and saga coordinator are outside this contract audit but require a separate
execution and key-management review.

## Security properties

1. No agent-controlled target, selector, route, recipient, arbitrary calldata, or declared notional
   reaches a funded execution path.
2. Only the configured agent executes; only the administrator configures or activates; the guardian
   cannot increase authority; recovery is halted-only and pays the configured Safe.
3. Intent IDs cannot be replayed. Deadline, configuration version, mode, and market state are checked
   atomically with execution.
4. The Stock Token oracle must be positive, complete, fresh, and unpaused. The L2 sequencer must be up
   beyond the grace period. Pending multiplier transitions prevent execution.
5. Risk accounting uses vault-observed token deltas. Per-order, turnover, inventory, gross exposure,
   and active-market limits cannot be bypassed by reentrancy, token behavior, or adapter output.
6. Routes are single-hop exact-input zero-hook pools configured by the administrator. Pool, direction,
   slippage floor, deadline, settlement, and output collection are constructed on-chain.
7. ERC-20 and Permit2 approvals are exact and cleared. The adapter cannot retain incremental input or
   output balances after a successful swap.
8. A failed swap, underfill, accounting mismatch, or post-trade limit failure reverts intent usage,
   transfers, approvals, and risk state atomically.
9. Deployment binds the same settlement asset and administrator across vault, risk manager, and
   adapter, creates the correct anchor publisher, and starts halted with no market.
10. Anchors are non-empty, append-only, and strictly ordered.

## Required audit evidence

- At least 95% overall branch coverage and 100% branch coverage for authorization, modes, caps,
  replay, recovery, approvals, and fund flow.
- Stateful invariants for recipient restriction, balance conservation, allowance cleanup, replay,
  mode monotonicity for the guardian, cap enforcement, inventory cost, and gross exposure.
- Fuzz tests for token decimals, oracle decimals, rounding, malicious tokens, router failure,
  reentrancy, sequencer outage, stale feeds, multiplier transitions, and version skew.
- A pinned Robinhood mainnet fork covering the real USDG, Stock Token, feed, sequencer feed, Permit2,
  Universal Router, PoolManager, and zero-hook pool.
- Recorded runtime code hashes and upstream commits for every external contract ABI.
- Formal verification of authorization, replay, recipient, mode, cap, and conservation properties.

## Technical release conditions

- No funding or execution-authority installation while an internal critical or high finding remains open.
- No capital activation until the execution and key review passes for the exact release.
- No activation if the deployed Universal Router ABI or runtime code hash differs from the pinned
  fork target.
- Emergency recovery permanently disables deposits and execution for the affected vault.

## Review deliverables

- Exact reviewed commit, compiler settings, external dependency commits, and runtime code hashes.
- Severity, impact, proof, and remediation guidance for every finding.
- Review of deployment, Safe batch, role separation, fork evidence, tests, and formal properties.
- A retest statement for every remediated critical, high, or medium finding.
