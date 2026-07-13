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
   `deployments/testnet-proof.json` only after onchain confirmation.
4. Fund the agent only with enough test ETH for attestation operations. The agent must not receive
   owner authority.
5. Run `npm run verify:testnet-proof` after each proof deployment.
6. Publish only after the local check suite, identity leak scan, and a review of staged files.

`Deploy.s.sol` creates a halted, no-venue core. It is not authorization to fund or enable
execution. Immediately run `VerifyDeployment.s.sol` against the confirmed addresses and retain its
output with the deployment record. A future typed venue adapter requires its own reviewed release
and cannot be substituted with a generic Universal Router call.

## Render release procedure

The Render service is Git-backed and deploys `main` automatically. The repository binding in
`.renderctl.json` is the source of truth for the intended workspace. Before any Render API action,
run `./scripts/renderctl guard`. After a push, confirm that the deploy references the expected
commit, reaches `live`, and that `https://robintheclaw.com` returns the new public copy.

The public site and authenticated application run in the `robintheclaw` Next.js service. The
browser receives only the public Privy app ID. Authenticated application requests use the
same-origin proxy to the private `robin-api` service. Sponsored wallet requests use a separate
same-origin proxy that validates the session and planned calls before adding server-held Alchemy
credentials.

`robin-api` is a private Rust service in the same region. It connects to the dedicated
`robin-app` Postgres database and receives the Privy secret, ES256 verification key, provider RPC,
sponsorship policy, and confirmed application contract addresses through managed settings. The
product database is separate from `robin-research`.

Before enabling onboarding:

1. Deploy `DeployUxTestnet.s.sol` on chain ID 46630 and confirm the asset, faucet, and factory.
2. Create an Alchemy sponsorship policy limited to those contracts, child vaults and guards, the
   `claim`, `approve`, `createVault`, `deposit`, `withdraw`, and `setHalted` selectors, with
   per-account and global spend quotas.
3. Configure all `sync: false` application values in Render. Use a provider RPC for `APP_RPC_URL`.
4. Configure Privy allowed origins, email/passkey login, Google and Apple OAuth, and embedded EVM
   wallet creation for all users.
5. Run an embedded-user onboarding smoke test, verify the factory receipt in `robin-api`, reload
   during confirmation, link two external wallets, change the funding source, pause, resume,
   deposit, withdraw, unlink, sign out, and recover the same account.
6. Confirm that dashboard values match provider RPC and that positions and P&L remain empty before
   real execution.
7. Confirm the security headers on the production domain and review structured `wallet_proxy_failed`
   and `app_proxy_failed` events during the smoke test without logging tokens, signatures, or calls.

The private collector is a separate Render worker named `robin-research-collector`. It has no
public URL. Its Postgres database allows no public IPs, and its R2 credentials are worker-only
managed secrets. Before provisioning or changing either service, run `./scripts/renderctl guard`.

Do not deploy the worker until `R2_BUCKET`, `AWS_ENDPOINT_URL`, `AWS_ACCESS_KEY_ID`, and
`AWS_SECRET_ACCESS_KEY` have been configured in managed settings. A failed archive write is a
source-health incident; do not substitute a local persistent disk for the immutable raw archive.

## Incident actions

| Event | Immediate action | Follow-up |
| --- | --- | --- |
| Unexpected agent call | Owner sets `MandateGuard.setHalted(true)`; preserve transaction evidence. | Rotate agent and investigate allowlist history. |
| Agent key exposure | Halt, rotate agent, and revoke any venue-specific credentials. | Reconcile all pending orders and anchors. |
| Owner key exposure | Treat custody as compromised; move assets only after a reviewed incident plan. | Deploy a new owner/vault boundary. |
| Signal API outage or stale feed | Do not create plans or orders. | Restore source health and document the gap. |
| Runtime archive or database failure | Stop treating captures as complete; preserve logs and mark the source degraded. | Restore both stores, reconcile gaps, and create a new dataset boundary. |
| Verifier mismatch | Stop publication claims and preserve the raw records. | Identify canonicalization, batch, or deployment mismatch before resuming. |
| Privy session expiry | Ask the user to sign in again; keep the saved onboarding call ID. | Confirm the operation after the session is restored. |
| Sponsored call pending | Preserve the call ID and show the pending state. | Recheck inclusion and confirm idempotently. |
| Included call, API delay | Do not prepare another vault. | Replay `vaults/confirm` from the saved call ID. |
| Wallet account conflict | Keep both user records separate. | Recover the account already linked to the wallet. |

No process may clear a chain halt without the owner. A software restart is not an incident remedy.
