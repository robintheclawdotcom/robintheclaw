// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Script } from "forge-std/Script.sol";
import { TimelockController } from "@openzeppelin/contracts/governance/TimelockController.sol";
import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { MandateRiskManagerV1 } from "../src/MandateRiskManagerV1.sol";
import { RwaDeploymentFactory } from "../src/RwaDeploymentFactory.sol";
import { RwaStrategyVault } from "../src/RwaStrategyVault.sol";
import { SequencerGate } from "../src/SequencerGate.sol";
import { UniswapV4SpotAdapter } from "../src/UniswapV4SpotAdapter.sol";

interface IVerifiedSafeProxy {
    function masterCopy() external view returns (address);
}

interface IVerifiedSafeAccount {
    function VERSION() external view returns (string memory);

    function getOwners() external view returns (address[] memory);

    function getThreshold() external view returns (uint256);
}

contract VerifyDeployment is Script {
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

    function run() external view {
        RwaDeploymentFactory factory = RwaDeploymentFactory(vm.envAddress("RWA_DEPLOYMENT_FACTORY"));
        RwaStrategyVault vault = RwaStrategyVault(vm.envAddress("RWA_STRATEGY_VAULT"));
        MandateRiskManagerV1 risk = MandateRiskManagerV1(vm.envAddress("MANDATE_RISK_MANAGER"));
        UniswapV4SpotAdapter adapter =
            UniswapV4SpotAdapter(vm.envAddress("UNISWAP_V4_SPOT_ADAPTER"));
        SequencerGate gate = SequencerGate(vm.envAddress("SEQUENCER_GATE"));
        TimelockController timelock = TimelockController(payable(vm.envAddress("TIMELOCK")));
        address safe = vm.envAddress("SAFE");
        address guardian = vm.envAddress("GUARDIAN");
        address asset = vm.envAddress("ASSET");

        require(block.chainid == 4663, "unexpected chain");
        require(asset == USDG, "unexpected asset");
        _verifyCode(factory, vault, risk, adapter, gate, timelock);
        _verifySafe(safe);
        _verifyTimelock(timelock, safe);

        require(address(factory.vault()) == address(vault), "factory vault mismatch");
        require(address(factory.riskManager()) == address(risk), "factory risk mismatch");
        require(address(factory.spotAdapter()) == address(adapter), "factory adapter mismatch");
        require(address(factory.sequencerGate()) == address(gate), "factory gate mismatch");
        require(factory.configAdmin() == address(timelock), "factory admin mismatch");
        require(factory.treasury() == safe, "factory treasury mismatch");
        require(factory.guardian() == guardian, "factory guardian mismatch");
        require(factory.initialAgent() == address(0), "factory agent not disabled");

        require(vault.configAdmin() == address(timelock), "vault admin mismatch");
        require(vault.treasury() == safe, "vault treasury mismatch");
        require(vault.agent() == address(0), "agent not disabled");
        require(!vault.recoveryFinalized(), "recovery already finalized");
        require(address(vault.settlementAsset()) == asset, "vault asset mismatch");
        require(address(vault.riskManager()) == address(risk), "vault risk mismatch");
        require(address(vault.spotAdapter()) == address(adapter), "vault adapter mismatch");

        require(risk.executor() == address(vault), "executor mismatch");
        require(risk.configAdmin() == address(timelock), "risk admin mismatch");
        require(risk.guardian() == guardian, "risk guardian mismatch");
        require(risk.treasury() == safe, "risk treasury mismatch");
        require(address(risk.settlementAsset()) == asset, "risk asset mismatch");
        require(risk.settlementDecimals() == 6, "risk asset decimals mismatch");
        require(address(risk.sequencerFeed()) == address(gate), "risk gate mismatch");
        require(risk.mode() == MandateRiskManagerV1.Mode.Halted, "not halted");
        require(risk.grossExposure() == 0, "gross exposure nonzero");
        require(risk.activeMarketCount() == 0, "active market count nonzero");
        require(risk.windowTurnover() == 0, "turnover nonzero");
        require(risk.pendingIntent() == bytes32(0), "pending intent exists");

        require(adapter.vault() == address(vault), "adapter vault mismatch");
        require(adapter.configAdmin() == address(timelock), "adapter admin mismatch");
        require(address(adapter.settlementAsset()) == asset, "adapter asset mismatch");
        require(address(adapter.router()) == vm.envAddress("UNIVERSAL_ROUTER"), "router mismatch");
        require(address(adapter.permit2()) == vm.envAddress("PERMIT2"), "permit2 mismatch");
        _verifyExternalCode(adapter);

        require(gate.configAdmin() == address(timelock), "gate admin mismatch");
        require(address(gate.source()) == address(0), "gate already bound");
        require(gate.expectedSourceCodeHash() == bytes32(0), "gate source hash set");
        (, int256 answer, uint256 startedAt, uint256 updatedAt,) = gate.latestRoundData();
        require(answer == 1, "unbound gate not down");
        require(
            startedAt == block.timestamp && updatedAt == block.timestamp, "gate timestamp mismatch"
        );

        _verifyLimits(risk);
        require(vault.attestationAnchor().publisher() == address(vault), "anchor mismatch");
        _verifyZeroBalances(factory, vault, risk, adapter, gate, asset);
    }

    function _verifyCode(
        RwaDeploymentFactory factory,
        RwaStrategyVault vault,
        MandateRiskManagerV1 risk,
        UniswapV4SpotAdapter adapter,
        SequencerGate gate,
        TimelockController timelock
    ) private view {
        require(
            address(factory).codehash == vm.envBytes32("RWA_DEPLOYMENT_FACTORY_CODEHASH"),
            "factory codehash mismatch"
        );
        require(
            address(vault).codehash == vm.envBytes32("RWA_STRATEGY_VAULT_CODEHASH"),
            "vault codehash mismatch"
        );
        require(
            address(risk).codehash == vm.envBytes32("MANDATE_RISK_MANAGER_CODEHASH"),
            "risk codehash mismatch"
        );
        require(
            address(adapter).codehash == vm.envBytes32("UNISWAP_V4_SPOT_ADAPTER_CODEHASH"),
            "adapter codehash mismatch"
        );
        require(
            address(gate).codehash == vm.envBytes32("SEQUENCER_GATE_CODEHASH"),
            "gate codehash mismatch"
        );
        require(
            address(vault.attestationAnchor()).codehash
                == vm.envBytes32("ATTESTATION_ANCHOR_CODEHASH"),
            "anchor codehash mismatch"
        );
        require(
            address(timelock).codehash == vm.envBytes32("TIMELOCK_CODEHASH"),
            "timelock codehash mismatch"
        );
    }

    function _verifySafe(address safe) private view {
        require(
            safe.codehash == vm.envBytes32("SAFE_PROXY_CODEHASH"), "Safe proxy codehash mismatch"
        );
        require(SAFE_L2.codehash == SAFE_L2_CODE_HASH, "SafeL2 code changed");
        require(IVerifiedSafeProxy(safe).masterCopy() == SAFE_L2, "unexpected Safe singleton");
        require(
            keccak256(bytes(IVerifiedSafeAccount(safe).VERSION())) == keccak256(bytes("1.5.0")),
            "unexpected Safe version"
        );

        address[] memory owners = IVerifiedSafeAccount(safe).getOwners();
        address owner1 = vm.envAddress("SAFE_OWNER_1");
        address owner2 = vm.envAddress("SAFE_OWNER_2");
        address owner3 = vm.envAddress("SAFE_OWNER_3");
        require(owners.length == 3, "Safe owner count != 3");
        require(IVerifiedSafeAccount(safe).getThreshold() == 2, "Safe threshold != 2");
        require(
            owner1 != address(0) && owner2 != address(0) && owner3 != address(0) && owner1 != owner2
                && owner1 != owner3 && owner2 != owner3,
            "invalid expected Safe owners"
        );
        require(
            _contains(owners, owner1) && _contains(owners, owner2) && _contains(owners, owner3),
            "Safe owners mismatch"
        );
    }

    function _verifyTimelock(TimelockController timelock, address safe) private view {
        require(timelock.getMinDelay() == vm.envUint("TIMELOCK_DELAY"), "delay mismatch");
        require(timelock.hasRole(timelock.PROPOSER_ROLE(), safe), "Safe not proposer");
        require(timelock.hasRole(timelock.CANCELLER_ROLE(), safe), "Safe not canceller");
        require(timelock.hasRole(timelock.EXECUTOR_ROLE(), safe), "Safe not executor");
        require(!timelock.hasRole(timelock.EXECUTOR_ROLE(), address(0)), "open executor");
        require(
            timelock.hasRole(timelock.DEFAULT_ADMIN_ROLE(), address(timelock)),
            "timelock not self-admin"
        );
        require(!timelock.hasRole(timelock.DEFAULT_ADMIN_ROLE(), safe), "Safe is admin");
        require(
            !timelock.hasRole(timelock.DEFAULT_ADMIN_ROLE(), address(0)), "zero address is admin"
        );
    }

    function _verifyExternalCode(UniswapV4SpotAdapter adapter) private view {
        bytes32 routerHash = vm.envBytes32("UNIVERSAL_ROUTER_CODEHASH");
        bytes32 permit2Hash = vm.envBytes32("PERMIT2_CODEHASH");
        require(address(adapter.router()) == CANONICAL_ROUTER, "unexpected router");
        require(address(adapter.permit2()) == CANONICAL_PERMIT2, "unexpected Permit2");
        require(routerHash == ROUTER_CODE_HASH, "unexpected router hash");
        require(permit2Hash == PERMIT2_CODE_HASH, "unexpected Permit2 hash");
        require(adapter.routerCodeHash() == routerHash, "router hash mismatch");
        require(adapter.permit2CodeHash() == permit2Hash, "permit2 hash mismatch");
        require(address(adapter.router()).codehash == routerHash, "router code changed");
        require(address(adapter.permit2()).codehash == permit2Hash, "permit2 code changed");
    }

    function _verifyLimits(MandateRiskManagerV1 risk) private view {
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
    }

    function _verifyZeroBalances(
        RwaDeploymentFactory factory,
        RwaStrategyVault vault,
        MandateRiskManagerV1 risk,
        UniswapV4SpotAdapter adapter,
        SequencerGate gate,
        address asset
    ) private view {
        require(IERC20(asset).balanceOf(address(vault)) == 0, "vault asset balance nonzero");
        require(IERC20(asset).balanceOf(address(adapter)) == 0, "adapter asset balance nonzero");
        require(address(factory).balance == 0, "factory ETH balance nonzero");
        require(address(vault).balance == 0, "vault ETH balance nonzero");
        require(address(risk).balance == 0, "risk ETH balance nonzero");
        require(address(adapter).balance == 0, "adapter ETH balance nonzero");
        require(address(gate).balance == 0, "gate ETH balance nonzero");
    }

    function _contains(address[] memory values, address expected) private pure returns (bool) {
        for (uint256 i; i < values.length; ++i) {
            if (values[i] == expected) return true;
        }
        return false;
    }
}
