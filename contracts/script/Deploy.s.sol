// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Script, console2 } from "forge-std/Script.sol";
import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { MandateGuard } from "../src/MandateGuard.sol";
import { StrategyVault } from "../src/StrategyVault.sol";
import { AttestationAnchor } from "../src/AttestationAnchor.sol";

/// @notice Deploys a production core. The asset and venue must be explicitly verified for the
/// target chain; there are deliberately no address defaults in this script.
contract Deploy is Script {
    error GenericVenueExecutionDisabled();

    function run() external {
        address owner = vm.envAddress("OWNER");
        address agent = vm.envAddress("AGENT");
        address asset = vm.envAddress("ASSET");
        uint256 cap = vm.envUint("WINDOW_CAP");
        uint64 window = uint64(vm.envUint("WINDOW_SECONDS"));

        require(block.chainid == 4663, "unexpected chain");
        require(owner != agent, "roles must differ");
        require(asset.code.length > 0, "asset has no code");
        require(cap > 0, "cap=0");
        require(window > 0, "window=0");
        if (vm.envOr("UNIVERSAL_ROUTER", address(0)) != address(0)) {
            revert GenericVenueExecutionDisabled();
        }

        vm.startBroadcast();

        MandateGuard guard = new MandateGuard(owner, owner, cap, window, true);
        StrategyVault vault = new StrategyVault(IERC20(asset), guard, owner, agent);
        AttestationAnchor anchor = new AttestationAnchor(address(vault));

        guard.setExecutor(address(vault));
        vault.setAttestationAnchor(anchor);
        vm.stopBroadcast();

        console2.log("MandateGuard     ", address(guard));
        console2.log("StrategyVault    ", address(vault));
        console2.log("AttestationAnchor", address(anchor));
        console2.log("owner            ", owner);
        console2.log("agent            ", agent);
        console2.log("asset            ", asset);
        console2.log("windowCap        ", cap);
        console2.log("windowSeconds    ", window);
        console2.log("execution        ", "locked");
    }
}
