# Operations and release runbook

## Roles

| Role | Responsibility | Required authority |
| --- | --- | --- |
| Safe treasury | Funds and recovers the production vault, revokes the agent immediately, restricts mode, and operates the timelock. | Canonical 2-of-3 Safe |
| Timelock | Installs the agent, loosens mode, configures limits, markets and routes, binds the sequencer source, and rotates the guardian. | Safe-proposed and Safe-executed after 48 hours |
| Guardian | Moves production mode only toward `REDUCE_ONLY` or `HALTED`. | Separate guardian key; no activation or withdrawal authority |
| Agent | Submits typed spot intents and record batches. | Dedicated KMS-backed execution key; currently zero |
| Deployer | Broadcasts immutable contract creation and receives no role. | Temporary fee-paying key only |
| Testnet owner | Controls one personal application vault and guard. | User smart account on chain 46630 |
| Operator | Runs read-only measurement and verification commands. | No signing key |

Keep production roles separate. The mainnet deployer is not a Safe owner, timelock administrator,
guardian, agent, or vault beneficiary. Do not use a browser profile as key storage. Rotate the
bootstrap Safe owner set to device-separated operational owners before capital activation.

## Deployment procedure

1. Run the repository checks in the developer guide and freeze the exact commit and compiler settings.
2. Confirm `config/addresses.json` and `deployments/mainnet.json` match the expected chain, roles,
   external contracts, runtime code hashes, and staged-activation state.
3. For a new production generation, verify or deploy the canonical Safe and timelock first, then
   rehearse `Deploy.s.sol` against a pinned current mainnet fork before broadcast.
4. Run `VerifyDeployment.s.sol` after inclusion, verify every source on Blockscout, confirm the
   Robinhood batch commitment is finalized on Ethereum, and retain the release evidence.
5. For testnet proof deployments, use `DeployTestnet.s.sol`; record generated addresses in
   `deployments/testnet-proof.json` only after onchain confirmation.
6. Fund the testnet agent only with enough test ETH for attestation operations. The agent must not receive
   owner authority.
7. Run `npm run verify:testnet-proof` after each proof deployment.
8. Publish only after the local check suite, identity leak scan, and a review of staged files.

The current production deployment completed this procedure at block `8829911`. It is halted and
unfunded, has a zero agent, and has no market, route, or sequencer source. Its addresses and
verification state are in [`deployments/mainnet.json`](../deployments/mainnet.json). Staged market
activation is a separate Safe and timelock release; a generic Universal Router call cannot
substitute for the typed adapter path.

## Render release procedure

The Render service is Git-backed and deploys `main` automatically. The repository binding in
`.renderctl.json` is the source of truth for the intended workspace. Before any Render API action,
run `./scripts/renderctl guard`. After a push, confirm that the deploy references the expected
commit, reaches `live`, and that `https://robintheclaw.com` returns the new public copy.

The public site and authenticated application run in the `robintheclaw` Next.js service. The
browser receives only the public Privy app ID. Authenticated application requests use the
same-origin proxy to the private `robin-api` service. Wallet requests use a separate same-origin
proxy that validates the session and planned calls before adding server-held Alchemy credentials.

`robin-api` is a private Rust service in the same region. It connects to the dedicated
`robin-app` Postgres database and receives the Privy secret, ES256 verification key, provider RPC,
and confirmed application contract addresses through managed settings. The
product database is separate from `robin-research`.

Current testnet resources:

| Resource | Plan | Role |
| --- | --- | --- |
| `robintheclaw` | Starter | Public website and authenticated application |
| `robin-api` | Starter | Private Rust application API and activity indexer |
| `robin-app` | Basic 1 GB | Dedicated application PostgreSQL database |

All three application resources run in the same Render region. The API and database are private.

Before enabling onboarding:

1. Confirm the chain ID, runtime bytecode, factory/faucet wiring, and addresses in
   `deployments/ux-testnet.json`.
2. Choose the gas mode. For self-funded operation, leave `ALCHEMY_POLICY_ID` unset and fund the
   embedded account with ETH on Robinhood Chain before onboarding. For sponsored operation, create
   an Alchemy policy limited to the application contracts, child vaults and guards, the `claim`,
   `approve`, `createVault`, `deposit`, `withdraw`, and `setHalted` selectors, with per-account and
   global quotas. Store only the resulting policy ID in the web service settings.
3. Configure all `sync: false` application values in Render. Use a provider RPC for `APP_RPC_URL`.
   Keep `PRODUCT_INDEXER_BLOCK_RANGE` within the provider's `eth_getLogs` limit; the testnet
   Blueprint uses Alchemy's 10,000-block PAYG range and a 50,000-block initial lookback.
4. Configure Privy allowed origins, email/passkey login, Google and Apple OAuth, and embedded EVM
   wallet creation for all users. No separately managed WalletConnect project is required for
   launch. Privy's SDK still uses its documented WalletConnect relay and verification domains for
   named mobile-wallet connections, so keep those domains in the CSP.
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
| Unexpected production agent call | Guardian or Safe sets `HALTED`; Safe revokes the agent; preserve transaction evidence. | Reconcile the vault, signer journal, venue orders, and intent history before any timelocked replacement. |
| Agent key exposure | Safe revokes the agent and sets `HALTED`; revoke venue-specific credentials. | Reconcile every pending order and transaction, then install a replacement only through the timelock. |
| Safe owner exposure | Preserve quorum with uncompromised owners and replace the affected owner through a reviewed Safe transaction. | Review timelock operations, vault balances, agent state, and recovery readiness. |
| Guardian exposure | Safe sets `HALTED`; timelock rotates the guardian. | Confirm no unauthorized restriction event or governance operation occurred. |
| Signal API outage or stale feed | Do not create plans or orders. | Restore source health and document the gap. |
| Runtime archive or database failure | Stop treating captures as complete; preserve logs and mark the source degraded. | Restore both stores, reconcile gaps, and create a new dataset boundary. |
| Verifier mismatch | Stop publication claims and preserve the raw records. | Identify canonicalization, batch, or deployment mismatch before resuming. |
| Privy session expiry | Ask the user to sign in again; keep the saved onboarding call ID. | Confirm the operation after the session is restored. |
| Sponsored call pending | Preserve the call ID and show the pending state. | Recheck inclusion and confirm idempotently. |
| Included call, API delay | Do not prepare another vault. | Replay `vaults/confirm` from the saved call ID. |
| Wallet account conflict | Keep both user records separate. | Recover the account already linked to the wallet. |

Only the timelock can loosen production mode. A software restart, service flag, or environment
change cannot clear the onchain halt.
