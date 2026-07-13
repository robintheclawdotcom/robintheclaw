// Deterministic, order-preserving Merkle tree over trade-record leaves. A trade log is a
// sequence, so pairs are hashed left-to-right without sorting (sequence is part of the
// commitment); an odd node is promoted unchanged to the next level. parent = keccak256(l ++ r).
// Both the prover and any independent verifier build the tree the same way, so the root is
// reproducible from the records alone.

import { keccak256, concat } from "viem";

function hashPair(a, b) {
  return keccak256(concat([a, b]));
}

export function buildRoot(leaves) {
  if (leaves.length === 0) throw new Error("empty batch");
  let level = leaves.slice();
  while (level.length > 1) {
    const next = [];
    for (let i = 0; i < level.length; i += 2) {
      next.push(i + 1 < level.length ? hashPair(level[i], level[i + 1]) : level[i]);
    }
    level = next;
  }
  return level[0];
}

/// Sibling path proving `index` is in the tree, for onchain / independent inclusion checks.
export function proof(leaves, index) {
  if (!Number.isInteger(index) || index < 0 || index >= leaves.length) {
    throw new Error("proof index is out of range");
  }
  const path = [];
  let idx = index;
  let level = leaves.slice();
  while (level.length > 1) {
    const next = [];
    for (let i = 0; i < level.length; i += 2) {
      const hasRight = i + 1 < level.length;
      if (i === idx || i + 1 === idx) {
        if (hasRight) path.push({ sibling: idx === i ? level[i + 1] : level[i], right: idx === i });
      }
      next.push(hasRight ? keccak256(concat([level[i], level[i + 1]])) : level[i]);
    }
    idx = Math.floor(idx / 2);
    level = next;
  }
  return path;
}

export function verifyProof(leaf, path, root) {
  let acc = leaf;
  for (const step of path) {
    acc = step.right ? keccak256(concat([acc, step.sibling])) : keccak256(concat([step.sibling, acc]));
  }
  return acc === root;
}
