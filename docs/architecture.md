# Architecture

## Purpose and scope

Robin turns cross-venue market structure into delta-neutral trade plans: buy one exposure, short
the matched exposure, and capture a qualified spot/perpetual basis. The architecture separates
market intelligence, research, planning, execution, and records so each part can improve
independently.

## Components

| Component | Responsibility | Trust level | May move value |
| --- | --- | --- | --- |
| `signal/` | Reads Uniswap v4 and Lighter public market data; writes local JSONL observations. | Untrusted input | No |
| `runtime/` | Captures high-frequency market and chain evidence, stores immutable raw payloads, and creates only shadow lifecycles. | Private research input | No |
| `engine/` | Produces deterministic approved/declined plans from supplied JSON. | Pure computation | No |
| `contracts/` | Provides the typed production strategy vault and the separate personal-vault testnet application path. | Onchain enforcement | Only after the relevant governance boundary activates an execution path |
| `verifier/` | Canonicalizes disclosed records, computes Merkle roots, and compares roots with chain state. | Public verification | No |
| `app/` | Authenticates users, persists account state, verifies receipts, and assembles dashboards. | Private product API | No signing authority |
| `web/` | Public site and authenticated no-code application. | User interface | Submits user-signed operations |

## Data and control flow

```text
public market and chain data
       |
       v
private runtime -> raw R2 archive + Postgres snapshots -> shadow lifecycle
       |                                                |
       v                                                v
signal observation ----------------------------> deterministic engine
                                                    |
                                                    v
                                      venue integration layer
                                                    |
                                                    v
             RwaStrategyVault -> MandateRiskManagerV1 -> UniswapV4SpotAdapter

published records -> verifier -> Merkle root -> StrategyVault.anchorBatch -> AttestationAnchor
```

The consumer application adds a second control flow:

```text
Privy identity -> embedded signer -> Alchemy EIP-7702 smart account
       |                                  |
       |                                  v
linked wallets                  PersonalStrategyVaultFactory
       |                                  |
       +---- portfolio + funding --------> personal vault
                                          |       |
                                          v       v
                                    MandateGuard  AttestationAnchor

Next.js application -> authenticated Rust API -> robin-app Postgres
        |                         |
        |                         +-> provider RPC + receipt verification
        +-> guarded Wallet API proxy -> Alchemy Wallet APIs
```

The Privy DID is the durable application identity. Its embedded EVM wallet supplies the stable
Alchemy smart-account address that owns one versioned personal vault. External wallets are linked
portfolio and funding sources; selecting or unlinking one cannot change vault ownership.

The runtime expands the research layer with high-frequency market evidence. The typed production
contract graph is live on Robinhood Chain mainnet and progressing through staged activation. The
separate application testnet connects identity, personal vaults, sponsorship, and the dashboard
without becoming a substitute for the production execution boundary.

## Contract relationships

`RwaStrategyVault` is the production custody boundary. It accepts USDG from the Safe treasury and
exposes only typed spot intents. The agent cannot select a target, selector, recipient, route,
calldata payload, or declared notional. Actual vault balance deltas determine settlement.

`MandateRiskManagerV1` authorizes each intent against a one-time ID, deadline, configuration
version, operating mode, sequencer state, oracle round and heartbeat, oracle pause, Stock Token
multiplier, slippage floor, order cap, inventory cap, turnover, freshly marked gross exposure, and
active-market count. Entry and exit permissions are independent; the Safe and guardian can
restrict mode immediately, while only the timelock can loosen or configure policy.

`UniswapV4SpotAdapter` builds one configured exact-input, zero-hook route internally. It pins the
canonical Universal Router and Permit2 code hashes, grants exact temporary approvals, clears them
after use, retains no incremental balance, and returns output to the vault. `SequencerGate` starts
unbound and reports down until the timelock binds a reviewed source.

The production `AttestationAnchor` accepts roots only from `RwaStrategyVault`. Strict sequencing
makes a previous root immutable once the next sequence has been accepted.

`PersonalStrategyVaultFactory` is the separate Robinhood testnet application path. It does not
replace or administer the typed mainnet graph. The factory
It derives one CREATE2 vault address per owner and factory version. Each `PersonalStrategyVault`
deploys its own guard and anchor during construction, accepts deposits from a selected funding
wallet, and reserves withdrawals, mandate control, and agent rotation for the smart-account owner.

`TestAssetFaucet` supports the one-click testnet path with one fixed claim per smart account. The
onboarding batch claims the asset, approves the predicted vault, creates it, and deposits in one
wallet operation.

The browser does not receive the Alchemy API key or sponsorship policy. The Next.js wallet proxy
validates the Privy session, signing account, Robinhood Chain, and requested calls against the
authenticated API. It strips client-supplied paymaster data and adds the server policy only when
one is configured. Without a policy, the embedded account pays the Wallet API operation fee in
ETH. With a policy, Alchemy adds provider-side target, selector, quota, and spend controls.

## Required invariants

- Production config authority is the timelock; treasury and recovery authority is the Safe; the
  guardian can only restrict; the agent can only submit typed intents.
- The production vault starts with agent zero, zero balances, halted mode, and no market or route.
- Risk-manager executor and adapter vault are the production vault; anchor publisher is the vault.
- Every production intent binds one asset, side, input, minimum output, deadline, config version,
  multiplier, and minimum oracle round.
- A halted mode, stale source, paused oracle, multiplier transition, slippage breach, or exhausted
  limit prevents execution before value can leave the vault.
- A record batch is order-preserving. Reordering, changing a field, or changing the batch length
  changes the root.
- Gross risk is measured across both neutral legs, not one leg in isolation.

## State ownership

Onchain state is limited to custody controls and Merkle commitments. Market observations,
calibration inputs, and record publication live off chain. This is intentional: storing prices,
order books, or full fills onchain would be costly and would not improve their source quality.
The public commitment prevents silently rewriting a disclosed batch; it does not make withheld
records available.
