// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import {Script, console2} from "forge-std/Script.sol";
import {IERC20} from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import {MandateGuard} from "../src/MandateGuard.sol";
import {StrategyVault} from "../src/StrategyVault.sol";
import {AttestationAnchor} from "../src/AttestationAnchor.sol";

/// @notice Deploys the Robin the Claw core and wires the mandate. Reads config from env with
///         safe defaults so it also runs as a pure simulation (`forge script`) with no chain.
///         On Robinhood Chain, USDG is the vault asset and the Uniswap Universal Router is the
///         first allowlisted execution target; the perp adapter is added once built.
contract Deploy is Script {
    // Robinhood Chain canonical addresses (config/addresses.json)
    address constant USDG = 0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168;
    address constant UNIVERSAL_ROUTER = 0x8876789976dEcBfCbBbe364623C63652db8C0904;

    function run() external {
        address owner = vm.envOr("OWNER", msg.sender);
        address agent = vm.envOr("AGENT", msg.sender);
        address asset = vm.envOr("ASSET", USDG);
        uint256 cap = vm.envOr("WINDOW_CAP", uint256(1_000e6)); // 1,000 USDG / window default
        uint64 window = uint64(vm.envOr("WINDOW_SECONDS", uint256(1 days)));

        vm.startBroadcast();

        MandateGuard guard = new MandateGuard(owner, owner, cap, window);
        StrategyVault vault = new StrategyVault(IERC20(asset), guard, owner, agent);
        AttestationAnchor anchor = new AttestationAnchor(address(vault));

        guard.setExecutor(address(vault));
        // allowlist the Universal Router's execute(bytes,bytes[],uint256) entrypoint
        guard.setAllowed(UNIVERSAL_ROUTER, bytes4(keccak256("execute(bytes,bytes[],uint256)")), true);

        vm.stopBroadcast();

        console2.log("MandateGuard     ", address(guard));
        console2.log("StrategyVault    ", address(vault));
        console2.log("AttestationAnchor", address(anchor));
        console2.log("owner            ", owner);
        console2.log("agent            ", agent);
        console2.log("asset            ", asset);
        console2.log("windowCap        ", cap);
        console2.log("windowSeconds    ", window);
    }
}
