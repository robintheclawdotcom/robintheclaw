// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Script } from "forge-std/Script.sol";
import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { AttestationAnchor } from "../src/AttestationAnchor.sol";
import { MandateGuard } from "../src/MandateGuard.sol";
import { StrategyVault } from "../src/StrategyVault.sol";

/// @notice Read-only verification for a dormant mainnet core deployment.
contract VerifyDeployment is Script {
    function run() external view {
        address owner = vm.envAddress("OWNER");
        address agent = vm.envAddress("AGENT");
        address asset = vm.envAddress("ASSET");
        uint256 cap = vm.envUint("WINDOW_CAP");
        uint64 window = uint64(vm.envUint("WINDOW_SECONDS"));
        MandateGuard guard = MandateGuard(vm.envAddress("MANDATE_GUARD"));
        StrategyVault vault = StrategyVault(vm.envAddress("STRATEGY_VAULT"));
        AttestationAnchor anchor = AttestationAnchor(vm.envAddress("ATTESTATION_ANCHOR"));

        require(block.chainid == 4663, "unexpected chain");
        require(address(guard).code.length > 0, "guard has no code");
        require(address(vault).code.length > 0, "vault has no code");
        require(address(anchor).code.length > 0, "anchor has no code");
        require(address(IERC20(asset)).code.length > 0, "asset has no code");
        require(owner != agent, "roles must differ");
        require(vault.owner() == owner, "vault owner mismatch");
        require(vault.agent() == agent, "vault agent mismatch");
        require(address(vault.asset()) == asset, "vault asset mismatch");
        require(address(vault.guard()) == address(guard), "vault guard mismatch");
        require(address(vault.attestationAnchor()) == address(anchor), "vault anchor mismatch");
        require(guard.owner() == owner, "guard owner mismatch");
        require(guard.executor() == address(vault), "guard executor mismatch");
        require(guard.windowNotionalCap() == cap, "guard cap mismatch");
        require(guard.windowLength() == window, "guard window mismatch");
        require(guard.halted(), "guard must remain halted");
        require(anchor.publisher() == address(vault), "anchor publisher mismatch");
    }
}
