// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { ERC20 } from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import { StrategyVault } from "../src/StrategyVault.sol";
import { MandateGuard } from "../src/MandateGuard.sol";
import { AttestationAnchor } from "../src/AttestationAnchor.sol";

contract MockUSDG is ERC20 {
    constructor() ERC20("Global Dollar", "USDG") {
        _mint(msg.sender, 1_000_000e6);
    }

    function decimals() public pure override returns (uint8) {
        return 6;
    }
}

contract MockDex {
    uint256 public lastAmount;
    bool public shouldRevert;

    function swap(uint256 amount) external returns (uint256) {
        require(!shouldRevert, "dex down");
        lastAmount = amount;
        return amount;
    }

    function setRevert(bool r) external {
        shouldRevert = r;
    }
}

contract StrategyVaultTest is Test {
    MockUSDG usdg;
    MandateGuard guard;
    StrategyVault vault;
    MockDex dex;
    AttestationAnchor anchor;

    address owner = makeAddr("owner");
    address agent = makeAddr("agent");
    bytes4 swapSel = MockDex.swap.selector;

    function setUp() public {
        usdg = new MockUSDG();
        dex = new MockDex();
        // deploy guard with a placeholder executor, then point it at the vault
        vm.startPrank(owner);
        guard = new MandateGuard(owner, owner, 1_000e6, 1 days, false);
        vault = new StrategyVault(usdg, guard, owner, agent);
        guard.setExecutor(address(vault));
        guard.setAllowed(address(dex), swapSel, true);
        anchor = new AttestationAnchor(address(vault));
        vault.setAttestationAnchor(anchor);
        vm.stopPrank();

        usdg.transfer(owner, 10_000e6);
        vm.startPrank(owner);
        usdg.approve(address(vault), type(uint256).max);
        vault.fund(5_000e6);
        vm.stopPrank();
    }

    function test_fundsCustodied() public view {
        assertEq(vault.balance(), 5_000e6);
    }

    function test_agentExecutesAllowedCall() public {
        bytes memory data = abi.encodeWithSelector(swapSel, 300e6);
        vm.prank(agent);
        vault.execute(address(dex), data, 300e6);
        assertEq(dex.lastAmount(), 300e6);
        assertEq(guard.windowSpent(), 300e6);
    }

    function test_nonAgentCannotExecute() public {
        bytes memory data = abi.encodeWithSelector(swapSel, 1);
        vm.prank(owner);
        vm.expectRevert(StrategyVault.NotAgent.selector);
        vault.execute(address(dex), data, 1);
    }

    function test_guardBlocksUnlistedTarget() public {
        MockDex evil = new MockDex();
        bytes memory data = abi.encodeWithSelector(swapSel, 1);
        vm.prank(agent);
        vm.expectRevert(
            abi.encodeWithSelector(MandateGuard.NotAllowed.selector, address(evil), swapSel)
        );
        vault.execute(address(evil), data, 1);
    }

    function test_guardBlocksOverCap() public {
        bytes memory data = abi.encodeWithSelector(swapSel, 1_500e6);
        vm.prank(agent);
        vm.expectRevert(abi.encodeWithSelector(MandateGuard.CapExceeded.selector, 1_500e6, 1_000e6));
        vault.execute(address(dex), data, 1_500e6);
    }

    function test_haltStopsExecution() public {
        vm.prank(owner);
        guard.setHalted(true);
        bytes memory data = abi.encodeWithSelector(swapSel, 100e6);
        vm.prank(agent);
        vm.expectRevert(MandateGuard.IsHalted.selector);
        vault.execute(address(dex), data, 100e6);
    }

    function test_bubblesTargetRevert() public {
        dex.setRevert(true);
        bytes memory data = abi.encodeWithSelector(swapSel, 100e6);
        vm.prank(agent);
        vm.expectRevert();
        vault.execute(address(dex), data, 100e6);
    }

    function test_rejectsCalldataWithoutSelector() public {
        vm.prank(agent);
        vm.expectRevert(StrategyVault.InvalidCalldata.selector);
        vault.execute(address(dex), hex"1234", 1);
    }

    function test_rejectsExternallyOwnedTarget() public {
        bytes memory data = abi.encodeWithSelector(swapSel, 1);
        vm.prank(agent);
        vm.expectRevert(StrategyVault.InvalidTarget.selector);
        vault.execute(makeAddr("externallyOwned"), data, 1);
    }

    function test_agentAnchorsBatchThroughVault() public {
        bytes32 root = keccak256("batch-1");
        vm.prank(agent);
        vault.anchorBatch(root, 1, 3);

        AttestationAnchor.Batch memory batch = anchor.latest();
        assertEq(batch.root, root);
        assertEq(batch.sequence, 1);
        assertEq(batch.tradeCount, 3);
    }

    function test_nonAgentCannotAnchorBatch() public {
        vm.prank(owner);
        vm.expectRevert(StrategyVault.NotAgent.selector);
        vault.anchorBatch(keccak256("batch-1"), 1, 3);
    }

    function test_cannotReplaceAnchor() public {
        AttestationAnchor replacement = new AttestationAnchor(address(vault));
        vm.prank(owner);
        vm.expectRevert(StrategyVault.AnchorAlreadySet.selector);
        vault.setAttestationAnchor(replacement);
    }

    function test_ownerFundsAndDefunds() public {
        vm.prank(owner);
        vault.defund(owner, 2_000e6);
        assertEq(vault.balance(), 3_000e6);
    }

    function test_ownerCannotDefundToZeroAddress() public {
        vm.prank(owner);
        vm.expectRevert(StrategyVault.InvalidTarget.selector);
        vault.defund(address(0), 1);
    }
}
