// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Script, console2 } from "forge-std/Script.sol";
import { PersonalStrategyVaultFactory } from "../src/PersonalStrategyVaultFactory.sol";
import { TestAssetFaucet } from "../src/testnet/TestAssetFaucet.sol";
import { TestUSDG } from "../src/testnet/TestUSDG.sol";

contract DeployUxTestnet is Script {
    function run() external {
        address deployer = vm.envAddress("DEPLOYER");
        address agent = vm.envAddress("AGENT");
        uint256 supply = vm.envOr("TEST_ASSET_SUPPLY", uint256(10_000_000e6));
        uint256 faucetSupply = vm.envOr("FAUCET_SUPPLY", uint256(9_000_000e6));
        uint256 claimAmount = vm.envOr("FAUCET_CLAIM_AMOUNT", uint256(1_000e6));
        uint256 cap = vm.envOr("WINDOW_CAP", uint256(1_000e6));
        uint64 window = uint64(vm.envOr("WINDOW_SECONDS", uint256(1 days)));

        require(block.chainid == 46630, "unexpected chain");
        require(deployer != address(0) && agent != address(0), "invalid roles");
        require(faucetSupply <= supply && claimAmount > 0, "invalid supply");

        vm.startBroadcast();

        TestUSDG asset = new TestUSDG(deployer, supply);
        TestAssetFaucet faucet = new TestAssetFaucet(asset, claimAmount);
        PersonalStrategyVaultFactory factory =
            new PersonalStrategyVaultFactory(asset, agent, cap, window);
        asset.transfer(address(faucet), faucetSupply);

        vm.stopBroadcast();

        console2.log("TestUSDG                    ", address(asset));
        console2.log("TestAssetFaucet             ", address(faucet));
        console2.log("PersonalStrategyVaultFactory", address(factory));
        console2.log("agent                       ", agent);
        console2.log("claimAmount                 ", claimAmount);
    }
}
