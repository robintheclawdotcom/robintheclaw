# External audit scope: core custody boundary v1

## Scope

- `contracts/src/AttestationAnchor.sol`
- `contracts/src/MandateGuard.sol`
- `contracts/src/StrategyVault.sol`
- `contracts/src/interfaces/IAttestationAnchor.sol`
- `contracts/script/Deploy.s.sol`
- `contracts/script/VerifyDeployment.s.sol`

The audit target is the repository commit supplied to the auditor. `TestUSDG` and
`DeployTestnet.s.sol` support the isolated proof path and are not in the mainnet custody scope.

## Intended deployment profile

`Deploy.s.sol` creates a single-owner, halted custody boundary on Robinhood Chain mainnet. It
creates no venue allowlist and rejects a generic Universal Router parameter. The deployment must
remain unfunded and halted until the audit, operational review, and typed-adapter release gates are
complete.

## Invariants to assess

1. Only the configured owner can fund, defund, change the agent, configure the anchor, or change
   the mandate.
2. Only the configured vault can consume mandate notional.
3. A halted mandate prevents every guarded execution.
4. The cap and rolling window cannot be bypassed through state transitions or reentrancy.
5. The vault cannot call an externally owned account or calldata without a selector.
6. The anchor accepts sequential, non-empty commitments only from its configured vault.
7. The deployment verifier detects an incorrect role, asset, limit, anchor, or non-halted state.

## Explicit exclusions

- No public deposits, shares, NAV calculation, or multi-user accounting.
- No typed spot or perpetual execution adapter.
- No live venue approvals, routing, orders, margin, liquidation, or fill reconciliation.
- No performance, profitability, or market-data claim.

## Auditor deliverables

- A report identifying the exact audited commit and compiler version.
- Severity, impact, and reproduction for every finding.
- A review of tests, deployment scripts, and intended deployment configuration.
- A retest statement for remediated findings, if any.
