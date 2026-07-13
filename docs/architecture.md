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
| `contracts/` | Holds each user's asset in a personal vault and bounds calls made by the configured agent. | Onchain enforcement | Only after the owner enables the mandate and selectors |
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
                     StrategyVault -> MandateGuard -> allowlisted venue call

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

Next.js same-origin route -> authenticated Rust API -> robin-app Postgres
                                      |
                                      +-> provider RPC + receipt verification
```

The Privy DID is the durable application identity. Its embedded EVM wallet supplies the stable
Alchemy smart-account address that owns one versioned personal vault. External wallets are linked
portfolio and funding sources; selecting or unlinking one cannot change vault ownership.

The runtime expands the research layer with high-frequency market evidence. The current testnet
foundation connects planning and contracts; the live execution path will add a venue adapter while
preserving the same custody and strategy-role relationships.

## Contract relationships

`MandateGuard` owns the execution policy. It stores a human owner, one executor, a rolling
notional cap, a halt flag, and an allowlist keyed by target and selector. Only its executor can
consume notional; only its owner can change policy.

`StrategyVault` is the executor. It has an immutable asset, guard, and owner, plus a rotatable
agent. The agent can invoke `execute` only after the guard approves the target, selector, and
notional. The vault rejects selector-less payloads and externally owned account targets. The owner
alone can fund, defund, rotate the agent, and set the one-time anchor.

`AttestationAnchor` accepts roots only from the vault. The agent calls `StrategyVault.anchorBatch`,
which forwards to the anchor. Strict sequencing makes a previous root immutable once the next
sequence has been accepted.

`PersonalStrategyVaultFactory` adds the application path without changing the proof contracts.
It derives one CREATE2 vault address per owner and factory version. Each `PersonalStrategyVault`
deploys its own guard and anchor during construction, accepts deposits from a selected funding
wallet, and reserves withdrawals, mandate control, and agent rotation for the smart-account owner.

`TestAssetFaucet` supports the one-click testnet path with one fixed claim per smart account. The
onboarding batch claims the asset, approves the predicted vault, creates it, and deposits in one
sponsored operation.

## Required invariants

- Owner and agent roles are distinct at deployment.
- Guard executor is the vault; anchor publisher is the vault; vault anchor is the anchor.
- A target must contain bytecode and a selector must be nonzero before it can be allowed.
- A halted guard or exhausted rolling cap prevents execution before the target call.
- A record batch is order-preserving. Reordering, changing a field, or changing the batch length
  changes the root.
- Gross risk is measured across both neutral legs, not one leg in isolation.

## State ownership

Onchain state is limited to custody controls and Merkle commitments. Market observations,
calibration inputs, and record publication live off chain. This is intentional: storing prices,
order books, or full fills onchain would be costly and would not improve their source quality.
The public commitment prevents silently rewriting a disclosed batch; it does not make withheld
records available.
