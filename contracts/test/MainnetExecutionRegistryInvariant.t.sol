// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { MainnetExecutionRegistry } from "../src/MainnetExecutionRegistry.sol";
import { IMainnetExecutionRegistry } from "../src/interfaces/IMainnetExecutionRegistry.sol";

contract InvariantRegistryAdmin { }

contract RegistryGuardianHandler is Test {
    MainnetExecutionRegistry public registry;
    uint8 public highestRestriction;

    function bind(MainnetExecutionRegistry registry_) external {
        require(address(registry) == address(0));
        registry = registry_;
        highestRestriction = uint8(registry_.globalMode());
    }

    function restrict(uint8 rawMode) external {
        IMainnetExecutionRegistry.Mode mode = IMainnetExecutionRegistry.Mode(rawMode % 3);
        uint8 beforeMode = uint8(registry.globalMode());
        try registry.restrictGlobalMode(mode) {
            uint8 afterMode = uint8(registry.globalMode());
            assertGe(afterMode, beforeMode);
            highestRestriction = afterMode;
        } catch { }
    }
}

contract MainnetExecutionRegistryInvariantTest is Test {
    MainnetExecutionRegistry private registry;
    RegistryGuardianHandler private handler;

    function setUp() public {
        vm.chainId(4663);
        InvariantRegistryAdmin admin = new InvariantRegistryAdmin();
        handler = new RegistryGuardianHandler();
        registry = new MainnetExecutionRegistry(address(admin), address(handler));
        vm.prank(address(admin));
        registry.setGlobalMode(IMainnetExecutionRegistry.Mode.Active);
        handler.bind(registry);
        targetContract(address(handler));
        bytes4[] memory selectors = new bytes4[](1);
        selectors[0] = handler.restrict.selector;
        targetSelector(FuzzSelector({ addr: address(handler), selectors: selectors }));
    }

    function invariant_guardianNeverMakesGlobalModeLessRestrictive() public view {
        assertEq(uint8(registry.globalMode()), handler.highestRestriction());
    }
}
