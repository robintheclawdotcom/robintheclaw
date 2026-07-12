// Read the root the agent actually anchored on Robinhood Chain and compare it to the root
// recomputed from published records. This closes the loop: it is not enough that the records are
// internally consistent, they must match what the agent committed on-chain. Needs only a public
// RPC and the AttestationAnchor address, no keys.

import { createPublicClient, http, getAddress } from "viem";
import { verify } from "./batch.mjs";

const anchorAbi = [
  {
    name: "latest", type: "function", stateMutability: "view", inputs: [],
    outputs: [{
      type: "tuple",
      components: [
        { name: "root", type: "bytes32" }, { name: "sequence", type: "uint64" },
        { name: "tradeCount", type: "uint64" }, { name: "timestamp", type: "uint64" },
      ],
    }],
  },
  {
    name: "batches", type: "function", stateMutability: "view", inputs: [{ type: "uint64" }],
    outputs: [
      { name: "root", type: "bytes32" }, { name: "sequence", type: "uint64" },
      { name: "tradeCount", type: "uint64" }, { name: "timestamp", type: "uint64" },
    ],
  },
];

export async function readAnchoredRoot({ rpc, anchor, sequence }) {
  const client = createPublicClient({ transport: http(rpc) });
  const address = getAddress(anchor);
  if (sequence === undefined) {
    const b = await client.readContract({ address, abi: anchorAbi, functionName: "latest" });
    return { root: b.root, sequence: Number(b.sequence), tradeCount: Number(b.tradeCount), timestamp: Number(b.timestamp) };
  }
  const [root, seq, tradeCount, timestamp] = await client.readContract({
    address, abi: anchorAbi, functionName: "batches", args: [BigInt(sequence)],
  });
  return { root, sequence: Number(seq), tradeCount: Number(tradeCount), timestamp: Number(timestamp) };
}

/// Full verification: recompute the root from published records and confirm it equals the root
/// anchored on-chain for that sequence.
export async function verifyAgainstChain({ rpc, anchor, sequence, records }) {
  const onchain = await readAnchoredRoot({ rpc, anchor, sequence });
  const local = verify(records, onchain.root);
  return {
    ok: local.ok && onchain.tradeCount === records.length,
    onchainRoot: onchain.root,
    recomputed: local.recomputed,
    tradeCountMatch: onchain.tradeCount === records.length,
    onchain,
    reason: local.ok
      ? (onchain.tradeCount === records.length ? "records match the on-chain root" : "root matches but trade count differs")
      : "recomputed root does not match the on-chain root",
  };
}
