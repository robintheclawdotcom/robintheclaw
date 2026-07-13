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
fixtures/                batch inputs and a regen script that rebuilds the committed proofs via the CLI
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

where `batch.json` describes the claim and the private trades:

```json
{
  "agentId": "0xa11ce",
  "thresholdBps": 10,
  "trades": [
    { "netPnlUsd": 0.20, "notionalUsd": 100.0 },
    { "netPnlUsd": 0.10, "notionalUsd": 100.0 }
  ]
}
```

`thresholdBps` is the claim: net return was at least this many basis points, or negative for a
max-loss claim. The CLI refuses to prove a batch whose net return is below the claim.

The output directory gets `proof` and `proof.hex` (the keccak proof, binary and hex),
`public_inputs`, `vk`, and `claim.json` (agent, threshold, trade count, net return, the Poseidon
commitment, and the blinding). To verify on-chain, pass `proof.hex` and the four public parameters
from `claim.json` (`agentId`, `thresholdBps`, `tradeCount`, `commitment`) to
`PnlProofVerifier.verifyPnlClaim`; the Foundry test does exactly this.

`blinding` is optional. Supply a fresh random field element, or omit it and the CLI generates one
and records it in `claim.json`. Never reuse a blinding across proofs, and keep `claim.json`: the
blinding is what reopens the commitment, and its secrecy is what stops an observer who guesses the
trades from confirming them.

## Toolchain

- Noir `nargo` 1.0.0-beta.22, Poseidon library `v0.3.0`.
- Barretenberg `bb` (UltraHonk). Proofs are generated with `--oracle_hash keccak`, so the same
  7616-byte proof verifies both natively (`bb verify`) and through the generated Solidity verifier.

## Notes and boundaries

- Amounts are scaled to integer micro-dollars before proving; no floats enter the witness.
- The circuit fixes a maximum of 32 trades per proof. Longer histories are proven as multiple
  batched claims.
- This directory verifies a claim about committed trades. It does not decide which trades belong in
  the batch, price them, or link the commitment to the anchored root — those are the consuming
  contract's job.
