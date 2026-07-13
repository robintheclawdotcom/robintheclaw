// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Script, console2 } from "forge-std/Script.sol";
import { MandateGuard } from "../src/MandateGuard.sol";
import { StrategyVault } from "../src/StrategyVault.sol";
import { AttestationAnchor } from "../src/AttestationAnchor.sol";
import { TestUSDG } from "../src/testnet/TestUSDG.sol";

/// @notice Deploys the isolated testnet proof path. It deliberately has no execution venue.
contract DeployTestnet is Script {
    function run() external {
        address owner = vm.envAddress("OWNER");
        address agent = vm.envAddress("AGENT");
        uint256 initialSupply = vm.envOr("TEST_ASSET_SUPPLY", uint256(10_000e6));
        uint256 cap = vm.envOr("WINDOW_CAP", uint256(1_000e6));
        uint64 window = uint64(vm.envOr("WINDOW_SECONDS", uint256(1 days)));

        require(block.chainid == 46630, "unexpected chain");
        require(owner != agent, "roles must differ");
        require(initialSupply > 0 && cap > 0, "invalid config");

        vm.startBroadcast();

        TestUSDG asset = new TestUSDG(owner, initialSupply);
        MandateGuard guard = new MandateGuard(owner, owner, cap, window, true);
        StrategyVault vault = new StrategyVault(asset, guard, owner, agent);
        AttestationAnchor anchor = new AttestationAnchor(address(vault));

        guard.setExecutor(address(vault));
        vault.setAttestationAnchor(anchor);

        vm.stopBroadcast();

        console2.log("TestUSDG         ", address(asset));
        console2.log("MandateGuard     ", address(guard));
        console2.log("StrategyVault    ", address(vault));
        console2.log("AttestationAnchor", address(anchor));
        console2.log("owner            ", owner);
        console2.log("agent            ", agent);
        console2.log("initialSupply    ", initialSupply);
    }
}
