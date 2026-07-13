// SPDX-License-Identifier: Apache-2.0
pragma solidity ^0.8.28;

import { HonkVerifier } from "./HonkVerifier.sol";

/// @title PnlProofVerifier
/// @notice Domain wrapper over the generated Honk verifier for proof-of-PnL claims. It names the
///         four public inputs the circuit exposes and checks the proof against them, so a caller
///         verifies "this agent's net return over `tradeCount` committed trades was at least
///         `thresholdBps`" without learning any trade. The commitment binds the proof to a
///         specific dataset; associating that commitment with the agent's on-chain anchored
///         record is done by the consuming contract, not here.
contract PnlProofVerifier {
    HonkVerifier public immutable verifier;

    constructor() {
        verifier = new HonkVerifier();
    }

    /// @param proof         the serialized Honk proof
    /// @param agentId       public: the agent identity the claim is about
    /// @param thresholdBps  public: the claimed minimum net return in basis points, negative for a max-loss claim
    /// @param tradeCount    public: the number of trades the claim covers
    /// @param commitment    public: the Poseidon commitment binding the private trades
    function verifyPnlClaim(
        bytes calldata proof,
        bytes32 agentId,
        int256 thresholdBps,
        uint256 tradeCount,
        bytes32 commitment
    ) external view returns (bool) {
        bytes32[] memory publicInputs = new bytes32[](4);
        publicInputs[0] = agentId;
        publicInputs[1] = encodeThreshold(thresholdBps);
        publicInputs[2] = bytes32(tradeCount);
        publicInputs[3] = commitment;
        return verifier.verify(proof, publicInputs);
    }

    /// The circuit's threshold is an i64. Noir serializes a signed i64 public input as its
    /// two's-complement u64 value, zero-extended to the field, so a negative threshold becomes
    /// 2^64 - |x| rather than a 256-bit sign extension. Match that exactly; the circuit's own
    /// range check keeps the value inside i64, so an out-of-range threshold simply has no proof.
    function encodeThreshold(int256 thresholdBps) internal pure returns (bytes32) {
        return bytes32(uint256(uint64(int64(thresholdBps))));
    }
}
