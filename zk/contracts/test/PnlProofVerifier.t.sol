// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { PnlProofVerifier } from "../PnlProofVerifier.sol";

/// Runs a real, pre-generated proof through the on-chain verifier. The proof attests that agent
/// 0xabcd cleared 100 bps net over 3 committed trades; the fixture is produced by the prover CLI
/// and committed under fixtures/. The test confirms the verifier accepts the honest proof and
/// rejects tampered public inputs.
contract PnlProofVerifierTest is Test {
    PnlProofVerifier internal wrapper;

    bytes internal proof;
    bytes32 internal constant AGENT = bytes32(uint256(0xabcd));
    int256 internal constant THRESHOLD_BPS = 100;
    uint256 internal constant TRADE_COUNT = 3;
    bytes32 internal constant COMMITMENT =
        0x1660769983d5dc238c6cb748c3313f0299baf9823e376e9a07b3551bdd5707a7;

    function setUp() public {
        wrapper = new PnlProofVerifier();
        proof = vm.parseBytes(vm.readFile("./fixtures/proof.hex"));
    }

    function test_acceptsAnHonestProof() public view {
        assertTrue(
            wrapper.verifyPnlClaim(proof, AGENT, THRESHOLD_BPS, TRADE_COUNT, COMMITMENT),
            "honest proof must verify"
        );
    }

    // The Honk verifier reverts on an inconsistent proof/public-input pair rather than returning
    // false, so a rejected claim is a revert. `verifyRejects` asserts the call does not succeed
    // with a true return, covering both the revert and the false-return cases.
    function verifyRejects(
        bytes memory proof_,
        bytes32 agentId,
        int256 thresholdBps,
        uint256 tradeCount,
        bytes32 commitment
    ) internal view returns (bool rejected) {
        try wrapper.verifyPnlClaim(proof_, agentId, thresholdBps, tradeCount, commitment) returns (bool ok) {
            return !ok;
        } catch {
            return true;
        }
    }

    function test_rejectsWrongAgent() public view {
        assertTrue(
            verifyRejects(proof, bytes32(uint256(0xdead)), THRESHOLD_BPS, TRADE_COUNT, COMMITMENT),
            "proof must not verify for a different agent"
        );
    }

    function test_rejectsWrongThreshold() public view {
        assertTrue(
            verifyRejects(proof, AGENT, 200, TRADE_COUNT, COMMITMENT),
            "proof must not verify against a different claimed threshold"
        );
    }

    function test_rejectsWrongCommitment() public view {
        assertTrue(
            verifyRejects(proof, AGENT, THRESHOLD_BPS, TRADE_COUNT, bytes32(uint256(1))),
            "proof must not verify against a different commitment"
        );
    }

    function test_rejectsTamperedProof() public view {
        bytes memory bad = proof;
        bad[bad.length - 1] = bytes1(uint8(bad[bad.length - 1]) ^ 0x01);
        assertTrue(
            verifyRejects(bad, AGENT, THRESHOLD_BPS, TRADE_COUNT, COMMITMENT),
            "a tampered proof must not verify"
        );
    }
}
