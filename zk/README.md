# zk — proof of PnL

A zero-knowledge proof that an agent's net return over a set of trades cleared a public threshold,
without revealing any individual trade. It lets the agent stand behind a performance claim ("net
return over these N trades was at least X basis points") that anyone can check, while keeping the
strategy that produced it private.

This is the privacy-preserving companion to the on-chain track record: the attestation anchor makes
the record tamper-proof and public; this makes a claim *about* the record checkable without
exposing the trades.

## What the circuit proves

Given private per-trade net PnL and notional, a trade count, and a blinding, and public inputs for
the agent identity, a claimed minimum net return in basis points, and the trade count, the circuit
enforces:

- the aggregate net return in basis points over the committed trades is at least the threshold
  (computed without division, so there is no rounding to exploit);
- padded array slots beyond the count are exactly zero, so no value hides past the count;
- every value is within bounds that keep the arithmetic inside the field.

It returns a Poseidon commitment binding the private trades to the proof, so a prover cannot swap in
a different dataset after the fact. A negative threshold expresses a maximum-loss claim ("lost no
more than X bps").

The claim covers whatever trades the prover commits to. Binding that commitment to the agent's
*complete* on-chain anchored record — so a claim cannot cherry-pick winners — is an integration step
performed by the consuming contract, outside this circuit.

## Layout

```
circuits/proof-of-pnl/   Noir circuit (src/main.nr) + in-circuit tests
prover/                  Node CLI that encodes a trade batch and drives nargo + bb
contracts/               generated Honk verifier + a domain wrapper + Foundry tests
fixtures/                a committed golden proof, public inputs, and verification keys
```

## Build and test

Circuit (Noir):

```bash
cd circuits/proof-of-pnl && nargo test
```

Prover encoding (Node, no network, no toolchain):

```bash
cd prover && npm test
```

On-chain verifier (Foundry) — runs a real committed proof through the generated verifier:

```bash
cd contracts && forge test
```

## Generating a proof

```bash
cd prover
node src/prove.mjs batch.json --out proof-output
```

where `batch.json` is:

```json
{
  "agentId": "0x...",
  "thresholdBps": 100,
  "blinding": "0x...",
  "trades": [
    { "netPnlUsd": 0.03, "notionalUsd": 100.0 },
    { "netPnlUsd": -0.01, "notionalUsd": 100.0 }
  ]
}
```

The CLI writes `proof`, `public_inputs`, `vk`, and a `claim.json` summary. The blinding must be a
fresh random field element per proof; reusing it, or omitting it, lets an observer who guesses the
trades confirm them against the commitment.

## Toolchain

- Noir `nargo` 1.0.0-beta.22, Poseidon library `v0.3.0`.
- Barretenberg `bb` (UltraHonk). The on-chain path uses `--oracle_hash keccak`, which is what the
  generated Solidity verifier and its 7616-byte proof / 1888-byte key expect; the native path uses
  the default oracle. The two proofs are not interchangeable.

## Notes and boundaries

- Amounts are scaled to integer micro-dollars before proving; no floats enter the witness.
- The circuit fixes a maximum of 32 trades per proof. Longer histories are proven as multiple
  batched claims.
- This directory verifies a claim about committed trades. It does not decide which trades belong in
  the batch, price them, or link the commitment to the anchored root — those are the consuming
  contract's job.
