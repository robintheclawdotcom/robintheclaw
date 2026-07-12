# verifier

The recompute-the-record tool. This is what makes the agent's track record verifiable instead of
a screenshot: anyone takes the agent's published trade log, rebuilds the Merkle root, and confirms
it matches the root the agent anchored on Robinhood Chain. A single altered field in any trade
changes its leaf, changes the root, and fails the check.

No keys, no trust in the operator. Just the published records, a public RPC, and this code.

## Run the self-test

```bash
npm install
node src/demo.mjs
```

The demo builds a small delta-neutral batch, anchors the root, then shows an independent verifier
recomputing it, rejecting a record tampered to claim a better fill, and checking a single-trade
inclusion proof.

## Pieces

- `record.mjs`: canonical, integer-scaled encoding of one delta-neutral trade (spot leg + perp
  leg + basis + residual delta). Deterministic: no floats enter the hash.
- `merkle.mjs`: order-preserving keccak256 Merkle tree (a trade log is a sequence, so pairs are
  not sorted). Root plus inclusion proofs.
- `batch.mjs`: `prove(records)` produces the root and the `AttestationAnchor.anchor` args;
  `verify(records, root)` recomputes and compares.
- `onchain.mjs`: reads the root the agent actually anchored (`AttestationAnchor.latest` /
  `batches`) and compares it to the recomputed root, closing the loop against the chain.

## Verifying a live batch

```js
import { verifyAgainstChain } from "./src/onchain.mjs";

const result = await verifyAgainstChain({
  rpc: "https://rpc.mainnet.chain.robinhood.com",
  anchor: "0x…",        // deployed AttestationAnchor
  sequence: 1,
  records: publishedRecords,
});
// result.ok === true only if the published records reproduce the on-chain root
```

## Testnet proof deployment

The tracked testnet proof deployment has no execution venue and therefore cannot place a trade.
Its single synthetic fixture proves only the vault-to-anchor-to-verifier path:

```bash
npm run verify:testnet-proof
```

The fixture explicitly declares that it is not a fill or performance record.

## Design note

The contract stores only the root, so publishing the record is the agent's choice: what is
enforced on-chain is that the agent cannot later change what it committed. Withholding the record
is visible (a root with no published batch), and a published batch that does not recompute to the
anchored root is provably dishonest. The point is not that the agent is forced to publish, but
that anything it does publish is checkable and nothing it committed can be quietly rewritten.
