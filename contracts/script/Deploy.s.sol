// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Script, console2 } from "forge-std/Script.sol";
import { TimelockController } from "@openzeppelin/contracts/governance/TimelockController.sol";
import { IERC20Metadata } from "@openzeppelin/contracts/token/ERC20/extensions/IERC20Metadata.sol";
import { SafeCast } from "@openzeppelin/contracts/utils/math/SafeCast.sol";
import { IPermit2AllowanceTransfer, IUniversalRouter } from "../src/interfaces/IUniswapV4.sol";
import { RwaDeploymentFactory } from "../src/RwaDeploymentFactory.sol";

interface IDeploySafeProxy {
    function masterCopy() external view returns (address);
}

interface IDeploySafeAccount {
    function VERSION() external view returns (string memory);

    function getOwners() external view returns (address[] memory);

    function getThreshold() external view returns (uint256);
}

contract Deploy is Script {
    using SafeCast for uint256;

    address private constant SAFE_L2 = 0xEdd160fEBBD92E350D4D398fb636302fccd67C7e;
    address private constant USDG = 0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168;
    address private constant CANONICAL_ROUTER = 0x8876789976dEcBfCbBbe364623C63652db8C0904;
    address private constant CANONICAL_PERMIT2 = 0x000000000022D473030F116dDEE9F6B43aC78BA3;
    bytes32 private constant SAFE_L2_CODE_HASH =
        0x180193227186ccb85316c94db1f0d156ed932b14712cfaac78901899178572dc;
    bytes32 private constant ROUTER_CODE_HASH =
        0x2ce6aaaf9f4151f5e1cbf774668772f17f532ae11b15e9284fd0a072a8b0fbde;
    bytes32 private constant PERMIT2_CODE_HASH =
        0x5208783f52488f7d3493e5e38311ab707c1d75457fe472a19b0b4d57d66a7fca;

    function run() external {
        TimelockController timelock = TimelockController(payable(vm.envAddress("TIMELOCK")));
        address safe = vm.envAddress("SAFE");
        address guardian = vm.envAddress("GUARDIAN");
        address asset = vm.envAddress("ASSET");
        address router = vm.envAddress("UNIVERSAL_ROUTER");
        address permit2 = vm.envAddress("PERMIT2");
        uint256 turnoverWindow = vm.envUint("TURNOVER_WINDOW");
        uint256 maxDeadlineDelay = vm.envUint("MAX_DEADLINE_DELAY");
        uint256 sequencerGracePeriod = vm.envUint("SEQUENCER_GRACE_PERIOD");
        uint256 maxActiveMarkets = vm.envUint("MAX_ACTIVE_MARKETS");

        require(block.chainid == 4663, "unexpected chain");
        require(address(timelock).code.length > 0 && safe.code.length > 0, "missing governance");
        require(
            address(timelock).codehash == vm.envBytes32("TIMELOCK_CODEHASH"),
            "timelock codehash mismatch"
        );
        require(safe.codehash == vm.envBytes32("SAFE_PROXY_CODEHASH"), "Safe codehash mismatch");
        _verifySafe(safe);
        require(guardian != address(0), "guardian=0");
        require(
            address(timelock) != safe && address(timelock) != guardian && safe != guardian,
            "roles overlap"
        );
        require(
            asset.code.length > 0 && router.code.length > 0 && permit2.code.length > 0,
            "missing code"
        );
        require(asset == USDG && IERC20Metadata(asset).decimals() == 6, "unexpected asset");
        require(router == CANONICAL_ROUTER && permit2 == CANONICAL_PERMIT2, "unexpected venue");
        require(router.codehash == ROUTER_CODE_HASH, "router code changed");
        require(permit2.codehash == PERMIT2_CODE_HASH, "Permit2 code changed");
        require(
            vm.envBytes32("UNIVERSAL_ROUTER_CODEHASH") == ROUTER_CODE_HASH,
            "router hash input mismatch"
        );
        require(
            vm.envBytes32("PERMIT2_CODEHASH") == PERMIT2_CODE_HASH, "Permit2 hash input mismatch"
        );
        require(timelock.getMinDelay() == vm.envUint("TIMELOCK_DELAY"), "timelock delay mismatch");
        require(timelock.hasRole(timelock.PROPOSER_ROLE(), safe), "safe not proposer");
        require(timelock.hasRole(timelock.CANCELLER_ROLE(), safe), "safe not canceller");
        require(timelock.hasRole(timelock.EXECUTOR_ROLE(), safe), "safe not executor");
        require(!timelock.hasRole(timelock.EXECUTOR_ROLE(), address(0)), "open executor");
        require(
            timelock.hasRole(timelock.DEFAULT_ADMIN_ROLE(), address(timelock)),
            "timelock not self-admin"
        );
        require(!timelock.hasRole(timelock.DEFAULT_ADMIN_ROLE(), safe), "safe is admin");
        require(turnoverWindow <= type(uint64).max, "turnover window overflow");
        require(maxDeadlineDelay <= type(uint64).max, "deadline delay overflow");
        require(sequencerGracePeriod <= type(uint64).max, "grace period overflow");
        require(maxActiveMarkets <= type(uint8).max, "market count overflow");

        vm.startBroadcast();
        RwaDeploymentFactory factory = new RwaDeploymentFactory(
            RwaDeploymentFactory.Config({
                settlementAsset: IERC20Metadata(asset),
                router: IUniversalRouter(router),
                permit2: IPermit2AllowanceTransfer(permit2),
                configAdmin: address(timelock),
                treasury: safe,
                guardian: guardian,
                agent: address(0),
                routerCodeHash: ROUTER_CODE_HASH,
                permit2CodeHash: PERMIT2_CODE_HASH,
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
        console2.log("SequencerGate", address(factory.sequencerGate()));
        console2.log("RiskManager", address(factory.riskManager()));
        console2.log("SpotAdapter", address(factory.spotAdapter()));
        console2.log("Vault", address(factory.vault()));
        console2.log("AttestationAnchor", address(factory.vault().attestationAnchor()));
        console2.log("Execution", "halted");
    }

    function _verifySafe(address safe) private view {
        require(SAFE_L2.codehash == SAFE_L2_CODE_HASH, "SafeL2 code changed");
        require(IDeploySafeProxy(safe).masterCopy() == SAFE_L2, "unexpected Safe singleton");
        require(
            keccak256(bytes(IDeploySafeAccount(safe).VERSION())) == keccak256(bytes("1.5.0")),
            "unexpected Safe version"
        );
        require(IDeploySafeAccount(safe).getOwners().length == 3, "Safe owner count != 3");
        require(IDeploySafeAccount(safe).getThreshold() == 2, "Safe threshold != 2");
    }
}
