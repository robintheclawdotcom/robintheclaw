// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { MandateGuard } from "../src/MandateGuard.sol";

contract MockTarget {
    function swap(bytes calldata) external { }
}

contract MandateGuardTest is Test {
    MandateGuard guard;
    address owner = makeAddr("owner");
    address vault = makeAddr("vault"); // the executor
    address target;
    bytes4 sel = MockTarget.swap.selector;

    function setUp() public {
        target = address(new MockTarget());
        guard = new MandateGuard(owner, vault, 1_000e6, 1 days, false);
        vm.prank(owner);
        guard.setAllowed(target, sel, true);
    }

    function test_allowsWithinMandate() public {
        vm.prank(vault);
        assertTrue(guard.check(target, sel, 400e6));
        assertEq(guard.windowSpent(), 400e6);
        assertEq(guard.remaining(), 600e6);
    }

    function test_onlyExecutorChecks() public {
        vm.prank(owner);
        vm.expectRevert(MandateGuard.NotExecutor.selector);
        guard.check(target, sel, 1);
    }

    function test_rejectsUnlistedTarget() public {
        vm.prank(vault);
        vm.expectRevert(
            abi.encodeWithSelector(MandateGuard.NotAllowed.selector, address(0xBAD), sel)
        );
        guard.check(address(0xBAD), sel, 1);
    }

    function test_enforcesCap() public {
        vm.prank(vault);
        guard.check(target, sel, 900e6);
        vm.prank(vault);
        vm.expectRevert(abi.encodeWithSelector(MandateGuard.CapExceeded.selector, 1_100e6, 1_000e6));
        guard.check(target, sel, 200e6);
    }

    function test_windowRolls() public {
        vm.prank(vault);
        guard.check(target, sel, 1_000e6);
        assertEq(guard.remaining(), 0);

        vm.warp(block.timestamp + 1 days + 1);
        vm.prank(vault);
        assertTrue(guard.check(target, sel, 1_000e6)); // fresh window
        assertEq(guard.windowSpent(), 1_000e6);
    }

    function test_haltBlocks() public {
        vm.prank(owner);
        guard.setHalted(true);
        vm.prank(vault);
        vm.expectRevert(MandateGuard.IsHalted.selector);
        guard.check(target, sel, 1);
    }

    function test_canStartHalted() public {
        MandateGuard haltedGuard = new MandateGuard(owner, vault, 1_000e6, 1 days, true);
        vm.prank(vault);
        vm.expectRevert(MandateGuard.IsHalted.selector);
        haltedGuard.check(target, sel, 1);
    }

    function test_onlyOwnerAdmin() public {
        vm.prank(vault);
        vm.expectRevert(MandateGuard.NotOwner.selector);
        guard.setHalted(true);
    }

    function test_rejectsTargetWithoutCode() public {
        vm.prank(owner);
        vm.expectRevert("target has no code");
        guard.setAllowed(makeAddr("externallyOwned"), sel, true);
    }
}
