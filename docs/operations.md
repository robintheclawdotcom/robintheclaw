# Operations and release runbook

## Roles

| Role | Responsibility | Required authority |
| --- | --- | --- |
| Owner | Funds/defunds custody, changes mandate, halts execution, rotates agent. | Owner key only |
| Agent | Submits guarded calls and batches attestations. | Agent key only |
| Funder | Distributes test ETH to role wallets. It must not be configured as owner or agent. | Funder key only |
| Operator | Runs read-only measurement and verification commands. | No signing key |

Keep role keys separate. A signer used to deploy core contracts must be the configured owner,
because owner-only wiring occurs in the same broadcast. Do not use a browser profile as key storage.

## Deployment procedure

1. Run the repository checks in the developer guide.
2. Confirm `config/addresses.json` has the expected chain status and current contract code at each
   configured mainnet address.
3. For testnet proof deployments, use `DeployTestnet.s.sol`; record generated addresses in
   `deployments/testnet-proof.json` only after on-chain confirmation.
4. Fund the agent only with enough test ETH for attestation operations. The agent must not receive
   owner authority.
5. Run `npm run verify:testnet-proof` after each proof deployment.
6. Publish only after the local check suite, identity leak scan, and a review of staged files.

## Render release procedure

The Render service is Git-backed and deploys `main` automatically. The repository binding in
`.renderctl.json` is the source of truth for the intended workspace. Before any Render API action,
run `./scripts/renderctl guard`. After a push, confirm that the deploy references the expected
commit, reaches `live`, and that `https://robintheclaw.com` returns the new public copy.

The public site is static documentation. It must not receive execution keys, exchange credentials,
wallet material, or private strategy thresholds.

## Incident actions

| Event | Immediate action | Follow-up |
| --- | --- | --- |
| Unexpected agent call | Owner sets `MandateGuard.setHalted(true)`; preserve transaction evidence. | Rotate agent and investigate allowlist history. |
| Agent key exposure | Halt, rotate agent, and revoke any venue-specific credentials. | Reconcile all pending orders and anchors. |
| Owner key exposure | Treat custody as compromised; move assets only after a reviewed incident plan. | Deploy a new owner/vault boundary. |
| Signal API outage or stale feed | Do not create plans or orders. | Restore source health and document the gap. |
| Verifier mismatch | Stop publication claims and preserve the raw records. | Identify canonicalization, batch, or deployment mismatch before resuming. |

No process may clear a chain halt without the owner. A software restart is not an incident remedy.
