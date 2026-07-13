// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Script, console2 } from "forge-std/Script.sol";
import { IERC20Metadata } from "@openzeppelin/contracts/token/ERC20/extensions/IERC20Metadata.sol";
import { SafeCast } from "@openzeppelin/contracts/utils/math/SafeCast.sol";
import { IChainlinkFeed } from "../src/interfaces/IChainlinkFeed.sol";
import { IPermit2AllowanceTransfer, IUniversalRouter } from "../src/interfaces/IUniswapV4.sol";
import { RwaDeploymentFactory } from "../src/RwaDeploymentFactory.sol";

contract Deploy is Script {
    using SafeCast for uint256;

    function run() external {
        address admin = vm.envAddress("ADMIN");
        address recoveryRecipient = vm.envAddress("RECOVERY_RECIPIENT");
        address guardian = vm.envAddress("GUARDIAN");
        address agent = vm.envAddress("AGENT");
        address asset = vm.envAddress("ASSET");
        address sequencerFeed = vm.envAddress("SEQUENCER_FEED");
        address router = vm.envAddress("UNIVERSAL_ROUTER");
        address permit2 = vm.envAddress("PERMIT2");
        uint256 turnoverWindow = vm.envUint("TURNOVER_WINDOW");
        uint256 maxDeadlineDelay = vm.envUint("MAX_DEADLINE_DELAY");
        uint256 sequencerGracePeriod = vm.envUint("SEQUENCER_GRACE_PERIOD");
        uint256 maxActiveMarkets = vm.envUint("MAX_ACTIVE_MARKETS");

        require(block.chainid == 4663, "unexpected chain");
        require(admin != guardian && admin != agent && guardian != agent, "roles overlap");
        require(
            recoveryRecipient != address(0) && recoveryRecipient != admin
                && recoveryRecipient != agent,
            "recovery role overlap"
        );
        require(
            asset.code.length > 0 && sequencerFeed.code.length > 0 && router.code.length > 0
                && permit2.code.length > 0,
            "missing code"
        );
        require(turnoverWindow <= type(uint64).max, "turnover window overflow");
        require(maxDeadlineDelay <= type(uint64).max, "deadline delay overflow");
        require(sequencerGracePeriod <= type(uint64).max, "grace period overflow");
        require(maxActiveMarkets <= type(uint8).max, "market count overflow");

        vm.startBroadcast();
        RwaDeploymentFactory factory = new RwaDeploymentFactory(
            RwaDeploymentFactory.Config({
                settlementAsset: IERC20Metadata(asset),
                sequencerFeed: IChainlinkFeed(sequencerFeed),
                router: IUniversalRouter(router),
                permit2: IPermit2AllowanceTransfer(permit2),
                admin: admin,
                recoveryRecipient: recoveryRecipient,
                guardian: guardian,
                agent: agent,
                routerCodeHash: vm.envBytes32("UNIVERSAL_ROUTER_CODEHASH"),
                permit2CodeHash: vm.envBytes32("PERMIT2_CODEHASH"),
                grossNotionalLimit: vm.envUint("GROSS_NOTIONAL_LIMIT"),
                turnoverLimit: vm.envUint("TURNOVER_LIMIT"),
                turnoverWindow: turnoverWindow.toUint64(),
                maxDeadlineDelay: maxDeadlineDelay.toUint64(),
                sequencerGracePeriod: sequencerGracePeriod.toUint64(),
                maxActiveMarkets: maxActiveMarkets.toUint8()
            })
        );
        vm.stopBroadcast();

        console2.log("Factory", address(factory));
        console2.log("RiskManager", address(factory.riskManager()));
        console2.log("SpotAdapter", address(factory.spotAdapter()));
        console2.log("Vault", address(factory.vault()));
        console2.log("AttestationAnchor", address(factory.vault().attestationAnchor()));
        console2.log("Execution", "halted");
    }
}
