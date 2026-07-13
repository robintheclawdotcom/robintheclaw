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
    /// @param thresholdBps  public: the claimed minimum net return in basis points (two's complement)
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
        publicInputs[1] = bytes32(uint256(thresholdBps));
        publicInputs[2] = bytes32(tradeCount);
        publicInputs[3] = commitment;
        return verifier.verify(proof, publicInputs);
    }
}
