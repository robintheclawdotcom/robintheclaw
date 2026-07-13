# Architecture

## Purpose and scope

The system is built to produce durable, risk-adjusted net returns from a market-neutral trade
shape: buy one exposure and short the matched exposure when a measured spot/perpetual basis
survives costs and risk limits. The implementation is split so an observation cannot become an
execution merely because a process has a key.

## Components

| Component | Responsibility | Trust level | May move value |
| --- | --- | --- | --- |
| `signal/` | Reads Uniswap v4 and Lighter public market data; writes local JSONL observations. | Untrusted input | No |
| `runtime/` | Captures high-frequency market and chain evidence, stores immutable raw payloads, and creates only shadow lifecycles. | Private research input | No |
| `engine/` | Produces deterministic approved/declined plans from supplied JSON. | Pure computation | No |
| `contracts/` | Holds one owner's asset and bounds calls made by the configured agent. | On-chain enforcement | Only after an owner allowlists a contract and selector |
| `verifier/` | Canonicalizes disclosed records, computes Merkle roots, and compares roots with chain state. | Public verification | No |
| `web/` | Static public documentation and proof status. | Informational | No |

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
                                    future, separately verified executor
                                                    |
                                                    v
                     StrategyVault -> MandateGuard -> allowlisted venue call

published records -> verifier -> Merkle root -> StrategyVault.anchorBatch -> AttestationAnchor
```

The executor does not exist yet. Shadow output is not an execution path. A live order system must
use separately reviewed typed adapters and must never bypass the vault.

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

## Required invariants

- Owner and agent roles are distinct at deployment.
- Guard executor is the vault; anchor publisher is the vault; vault anchor is the anchor.
- A target must contain bytecode and a selector must be nonzero before it can be allowed.
- A halted guard or exhausted rolling cap prevents execution before the target call.
- A record batch is order-preserving. Reordering, changing a field, or changing the batch length
  changes the root.
- Gross risk is measured across both neutral legs, not one leg in isolation.

## State ownership

On-chain state is limited to custody controls and Merkle commitments. Market observations,
calibration inputs, and record publication live off chain. This is intentional: storing prices,
order books, or full fills on chain would be costly and would not improve their source quality.
The public commitment prevents silently rewriting a disclosed batch; it does not make withheld
records available.
