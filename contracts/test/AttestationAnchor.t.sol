// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { AttestationAnchor } from "../src/AttestationAnchor.sol";

contract AttestationAnchorTest is Test {
    AttestationAnchor anchor;
    address publisher = address(0xA11CE);
    address stranger = address(0xBEEF);

    function setUp() public {
        anchor = new AttestationAnchor(publisher);
    }

    function test_anchorsInSequence() public {
        vm.prank(publisher);
        anchor.anchor(bytes32(uint256(1)), 1, 10);
        assertEq(anchor.head(), 1);

        vm.prank(publisher);
        anchor.anchor(bytes32(uint256(2)), 2, 25);
        assertEq(anchor.head(), 2);

        AttestationAnchor.Batch memory b = anchor.latest();
        assertEq(b.root, bytes32(uint256(2)));
        assertEq(b.tradeCount, 25);
    }

    function test_onlyPublisher() public {
        vm.prank(stranger);
        vm.expectRevert(AttestationAnchor.NotPublisher.selector);
        anchor.anchor(bytes32(uint256(1)), 1, 1);
    }

    function test_rejectsEmptyRoot() public {
        vm.prank(publisher);
        vm.expectRevert(AttestationAnchor.EmptyRoot.selector);
        anchor.anchor(bytes32(0), 1, 1);
    }

    function test_rejectsOutOfOrder() public {
        vm.prank(publisher);
        vm.expectRevert(abi.encodeWithSelector(AttestationAnchor.BadSequence.selector, 1, 2));
        anchor.anchor(bytes32(uint256(9)), 2, 1);
    }

    function test_cannotRewriteHistory() public {
        vm.prank(publisher);
        anchor.anchor(bytes32(uint256(1)), 1, 10);
        // re-anchoring sequence 1 fails: next expected is 2
        vm.prank(publisher);
        vm.expectRevert(abi.encodeWithSelector(AttestationAnchor.BadSequence.selector, 2, 1));
        anchor.anchor(bytes32(uint256(99)), 1, 10);
    }

    function testFuzz_monotonicSequence(uint8 n) public {
        vm.assume(n > 0 && n <= 50);
        for (uint64 i = 1; i <= n; i++) {
            vm.prank(publisher);
            anchor.anchor(bytes32(uint256(i)), i, i);
        }
        assertEq(anchor.head(), n);
    }
}
