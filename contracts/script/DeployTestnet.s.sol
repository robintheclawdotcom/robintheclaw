// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Script, console2 } from "forge-std/Script.sol";
import { SyntheticProofVault } from "../src/testnet/SyntheticProofVault.sol";
import { TestUSDG } from "../src/testnet/TestUSDG.sol";

contract DeployTestnet is Script {
    function run() external {
        address owner = vm.envAddress("OWNER");
        address agent = vm.envAddress("AGENT");
        require(block.chainid == 46630, "unexpected chain");
        require(owner != agent, "roles overlap");

        vm.startBroadcast();
        TestUSDG asset = new TestUSDG(owner, vm.envOr("TEST_ASSET_SUPPLY", uint256(10_000e6)));
        SyntheticProofVault vault = new SyntheticProofVault(asset, owner, agent);
        vm.stopBroadcast();

        console2.log("TestUSDG", address(asset));
        console2.log("SyntheticProofVault", address(vault));
        console2.log("AttestationAnchor", address(vault.anchor()));
        console2.log("Trading", "not implemented");
    }
}
