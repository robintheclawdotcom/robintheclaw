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
    bytes4 constant UNIVERSAL_ROUTER_EXECUTE = bytes4(keccak256("execute(bytes,bytes[],uint256)"));

    function run() external {
        address owner = vm.envAddress("OWNER");
        address agent = vm.envAddress("AGENT");
        address asset = vm.envAddress("ASSET");
        address universalRouter = vm.envOr("UNIVERSAL_ROUTER", address(0));
        uint256 cap = vm.envOr("WINDOW_CAP", uint256(1_000e6)); // 1,000 USDG / window default
        uint64 window = uint64(vm.envOr("WINDOW_SECONDS", uint256(1 days)));

        require(block.chainid == vm.envOr("CHAIN_ID", uint256(4663)), "unexpected chain");
        require(owner != agent, "roles must differ");
        require(asset.code.length > 0, "asset has no code");
        require(
            universalRouter == address(0) || universalRouter.code.length > 0, "router has no code"
        );
        require(cap > 0, "cap=0");

        vm.startBroadcast();

        MandateGuard guard = new MandateGuard(owner, owner, cap, window);
        StrategyVault vault = new StrategyVault(IERC20(asset), guard, owner, agent);
        AttestationAnchor anchor = new AttestationAnchor(address(vault));

        guard.setExecutor(address(vault));
        vault.setAttestationAnchor(anchor);
        if (universalRouter != address(0)) {
            guard.setAllowed(universalRouter, UNIVERSAL_ROUTER_EXECUTE, true);
        }

        vm.stopBroadcast();

        console2.log("MandateGuard     ", address(guard));
        console2.log("StrategyVault    ", address(vault));
        console2.log("AttestationAnchor", address(anchor));
        console2.log("owner            ", owner);
        console2.log("agent            ", agent);
        console2.log("asset            ", asset);
        console2.log("universalRouter  ", universalRouter);
        console2.log("windowCap        ", cap);
        console2.log("windowSeconds    ", window);
    }
}
