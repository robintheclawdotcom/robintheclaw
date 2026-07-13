// Prove and verify a batch of trades. prove() turns a list of trade records into a Merkle root
// plus the exact args to anchor it onchain (AttestationAnchor.anchor). verify() independently
// recomputes the root from the published records and confirms it matches the claimed/anchored
// root: this is the "recompute the record" the whole product rests on. A single flipped field in
// any record changes its leaf and breaks the root, so a tampered log cannot pass.

import { record, leaf } from "./record.mjs";
import { buildRoot, proof, verifyProof } from "./merkle.mjs";

export function prove(rawRecords, sequence) {
  const records = rawRecords.map(record);
  const leaves = records.map(leaf);
  const root = buildRoot(leaves);
  return {
    root,
    anchor: { root, sequence: Number(sequence), tradeCount: records.length },
    leaves,
  };
}

/// Recompute the root from published records and check it equals the claimed root. Returns a
/// detailed result rather than a bare boolean so a verifier UI can show exactly what failed.
export function verify(rawRecords, claimedRoot) {
  let recomputed;
  try {
    recomputed = prove(rawRecords, 0).root;
  } catch (e) {
    return { ok: false, reason: e.message };
  }
  return {
    ok: recomputed === claimedRoot,
    recomputed,
    claimed: claimedRoot,
    tradeCount: rawRecords.length,
    reason: recomputed === claimedRoot ? "match" : "root mismatch: records do not produce the claimed root",
  };
}

/// Prove one record is in the batch without revealing the rest (inclusion proof).
export function proveInclusion(rawRecords, index) {
  const leaves = rawRecords.map(record).map(leaf);
  return { leaf: leaves[index], path: proof(leaves, index), root: buildRoot(leaves) };
}

export function checkInclusion(leafHash, path, root) {
  return verifyProof(leafHash, path, root);
}
