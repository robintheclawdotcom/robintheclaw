// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { PersonalStrategyVault } from "../src/PersonalStrategyVault.sol";
import { PersonalStrategyVaultFactory } from "../src/PersonalStrategyVaultFactory.sol";
import { TestAssetFaucet } from "../src/testnet/TestAssetFaucet.sol";
import { TestUSDG } from "../src/testnet/TestUSDG.sol";

contract PersonalStrategyVaultTest is Test {
    address owner = address(0xA11CE);
    address agent = address(0xA63E7);
    address depositor = address(0xD3F0517);

    TestUSDG asset;
    PersonalStrategyVaultFactory factory;
    TestAssetFaucet faucet;

    function setUp() public {
        vm.chainId(46630);
        asset = new TestUSDG(address(this), 1_000_000e6);
        factory = new PersonalStrategyVaultFactory(asset, agent, 1_000e6, 1 days);
        faucet = new TestAssetFaucet(asset, 1_000e6);
        asset.transfer(address(faucet), 100_000e6);
        asset.transfer(depositor, 10_000e6);
    }

    function testCreatesDeterministicWiredVault() public {
        address predicted = factory.predictVault(owner);

        vm.prank(owner);
        address deployed = factory.createVault();

        assertEq(deployed, predicted);
        assertEq(factory.vaultOf(owner), deployed);

        PersonalStrategyVault vault = PersonalStrategyVault(deployed);
        assertEq(address(vault.asset()), address(asset));
        assertEq(vault.owner(), owner);
        assertEq(vault.agent(), agent);
        assertEq(vault.guard().owner(), owner);
        assertEq(vault.guard().executor(), deployed);
        assertTrue(vault.guard().halted());
        assertEq(vault.attestationAnchor().publisher(), deployed);
    }

    function testRejectsDuplicateVault() public {
        vm.startPrank(owner);
        address deployed = factory.createVault();
        vm.expectRevert(
            abi.encodeWithSelector(PersonalStrategyVaultFactory.VaultExists.selector, deployed)
        );
        factory.createVault();
        vm.stopPrank();
    }

    function testDifferentOwnersReceiveDifferentVaults() public {
        address secondOwner = address(0xB0B);
        vm.prank(owner);
        address first = factory.createVault();
        vm.prank(secondOwner);
        address second = factory.createVault();
        assertNotEq(first, second);
    }

    function testFundingWalletCanDepositButOnlyOwnerCanWithdraw() public {
        vm.prank(owner);
        PersonalStrategyVault vault = PersonalStrategyVault(factory.createVault());

        vm.startPrank(depositor);
        asset.approve(address(vault), 500e6);
        vault.deposit(500e6);
        vm.expectRevert(PersonalStrategyVault.NotOwner.selector);
        vault.withdraw(depositor, 1e6);
        vm.stopPrank();

        vm.prank(owner);
        vault.withdraw(owner, 200e6);
        assertEq(asset.balanceOf(address(vault)), 300e6);
        assertEq(asset.balanceOf(owner), 200e6);
    }

    function testOneBatchOnboardingSequence() public {
        address predicted = factory.predictVault(owner);

        vm.startPrank(owner);
        faucet.claim();
        asset.approve(predicted, 1_000e6);
        factory.createVault();
        PersonalStrategyVault(predicted).deposit(1_000e6);
        vm.stopPrank();

        assertEq(asset.balanceOf(predicted), 1_000e6);
        assertEq(asset.balanceOf(owner), 0);
    }

    function testFaucetRejectsSecondClaim() public {
        vm.startPrank(owner);
        faucet.claim();
        vm.expectRevert(TestAssetFaucet.AlreadyClaimed.selector);
        faucet.claim();
        vm.stopPrank();
    }

    function testRejectsFactoryDeploymentOutsideRobinhoodTestnet() public {
        vm.chainId(4663);
        vm.expectRevert(
            abi.encodeWithSelector(PersonalStrategyVaultFactory.UnsupportedChain.selector, 4663)
        );
        new PersonalStrategyVaultFactory(asset, agent, 1_000e6, 1 days);
    }

    function testRejectsDirectVaultDeploymentOutsideRobinhoodTestnet() public {
        vm.chainId(4663);
        vm.expectRevert(
            abi.encodeWithSelector(PersonalStrategyVault.UnsupportedChain.selector, 4663)
        );
        new PersonalStrategyVault(asset, owner, agent, 1_000e6, 1 days);
    }
}
