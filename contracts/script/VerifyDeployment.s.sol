// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Script } from "forge-std/Script.sol";
import { MandateRiskManagerV1 } from "../src/MandateRiskManagerV1.sol";
import { RwaStrategyVault } from "../src/RwaStrategyVault.sol";
import { UniswapV4SpotAdapter } from "../src/UniswapV4SpotAdapter.sol";

contract VerifyDeployment is Script {
    function run() external view {
        RwaStrategyVault vault = RwaStrategyVault(vm.envAddress("RWA_STRATEGY_VAULT"));
        MandateRiskManagerV1 risk = MandateRiskManagerV1(vm.envAddress("MANDATE_RISK_MANAGER"));
        UniswapV4SpotAdapter adapter =
            UniswapV4SpotAdapter(vm.envAddress("UNISWAP_V4_SPOT_ADAPTER"));

        require(block.chainid == 4663, "unexpected chain");
        require(address(vault).code.length > 0, "vault has no code");
        require(address(risk).code.length > 0, "risk manager has no code");
        require(address(adapter).code.length > 0, "adapter has no code");
        require(vault.admin() == vm.envAddress("ADMIN"), "vault admin mismatch");
        require(
            vault.recoveryRecipient() == vm.envAddress("RECOVERY_RECIPIENT"),
            "recovery recipient mismatch"
        );
        require(vault.agent() == vm.envAddress("AGENT"), "agent mismatch");
        require(address(vault.settlementAsset()) == vm.envAddress("ASSET"), "asset mismatch");
        require(address(vault.riskManager()) == address(risk), "risk mismatch");
        require(address(vault.spotAdapter()) == address(adapter), "adapter mismatch");
        require(risk.executor() == address(vault), "executor mismatch");
        require(risk.admin() == vm.envAddress("ADMIN"), "risk admin mismatch");
        require(risk.guardian() == vm.envAddress("GUARDIAN"), "guardian mismatch");
        require(address(risk.settlementAsset()) == vm.envAddress("ASSET"), "risk asset mismatch");
        require(
            address(risk.sequencerFeed()) == vm.envAddress("SEQUENCER_FEED"),
            "sequencer feed mismatch"
        );
        require(adapter.vault() == address(vault), "adapter vault mismatch");
        require(adapter.admin() == vm.envAddress("ADMIN"), "adapter admin mismatch");
        require(
            address(adapter.settlementAsset()) == vm.envAddress("ASSET"), "adapter asset mismatch"
        );
        require(address(adapter.router()) == vm.envAddress("UNIVERSAL_ROUTER"), "router mismatch");
        require(address(adapter.permit2()) == vm.envAddress("PERMIT2"), "permit2 mismatch");
        bytes32 routerHash = vm.envBytes32("UNIVERSAL_ROUTER_CODEHASH");
        bytes32 permit2Hash = vm.envBytes32("PERMIT2_CODEHASH");
        require(adapter.routerCodeHash() == routerHash, "router hash mismatch");
        require(adapter.permit2CodeHash() == permit2Hash, "permit2 hash mismatch");
        require(address(adapter.router()).codehash == routerHash, "router code changed");
        require(address(adapter.permit2()).codehash == permit2Hash, "permit2 code changed");
        require(
            risk.grossNotionalLimit() == vm.envUint("GROSS_NOTIONAL_LIMIT"), "gross limit mismatch"
        );
        require(risk.turnoverLimit() == vm.envUint("TURNOVER_LIMIT"), "turnover limit mismatch");
        require(risk.turnoverWindow() == vm.envUint("TURNOVER_WINDOW"), "turnover window mismatch");
        require(
            risk.maxDeadlineDelay() == vm.envUint("MAX_DEADLINE_DELAY"), "deadline delay mismatch"
        );
        require(
            risk.sequencerGracePeriod() == vm.envUint("SEQUENCER_GRACE_PERIOD"),
            "sequencer grace mismatch"
        );
        require(
            risk.maxActiveMarkets() == vm.envUint("MAX_ACTIVE_MARKETS"), "market count mismatch"
        );
        require(risk.mode() == MandateRiskManagerV1.Mode.Halted, "not halted");
        require(vault.attestationAnchor().publisher() == address(vault), "anchor mismatch");
    }
}
