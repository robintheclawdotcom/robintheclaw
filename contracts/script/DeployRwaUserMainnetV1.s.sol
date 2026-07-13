// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Script, console2 } from "forge-std/Script.sol";
import { MainnetExecutionRegistry } from "../src/MainnetExecutionRegistry.sol";
import { QuorumSequencerFeed } from "../src/QuorumSequencerFeed.sol";
import { RwaUserVaultFactoryV1 } from "../src/RwaUserVaultFactoryV1.sol";
import { IChainlinkFeed } from "../src/interfaces/IChainlinkFeed.sol";
import { IMainnetExecutionRegistry } from "../src/interfaces/IMainnetExecutionRegistry.sol";
import { PoolKey } from "../src/interfaces/IUniswapV4.sol";
import { IRwaUserVaultFactoryV1 } from "../src/interfaces/IRwaUserVaultFactoryV1.sol";

interface IReleaseTimelock {
    function getMinDelay() external view returns (uint256);
}

contract DeployRwaUserMainnetV1 is Script {
    uint256 internal constant CHAIN_ID = 4663;

    address internal constant USDG = 0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168;
    address internal constant AAPL = 0xaF3D76f1834A1d425780943C99Ea8A608f8a93f9;
    address internal constant UNIVERSAL_ROUTER = 0x8876789976dEcBfCbBbe364623C63652db8C0904;
    address internal constant PERMIT2 = 0x000000000022D473030F116dDEE9F6B43aC78BA3;

    bytes32 internal constant USDG_CODE_HASH =
        0x864cc9ad53b338b82da1f7cab85ab0b3d5c8861acb422b6fec63cf36234f36a6;
    bytes32 internal constant AAPL_CODE_HASH =
        0x6c1fdd40002dcb440c7fff6a84171404d279ccb057803b65826f7546acd65630;
    bytes32 internal constant ROUTER_CODE_HASH =
        0x2ce6aaaf9f4151f5e1cbf774668772f17f532ae11b15e9284fd0a072a8b0fbde;
    bytes32 internal constant PERMIT2_CODE_HASH =
        0x5208783f52488f7d3493e5e38311ab707c1d75457fe472a19b0b4d57d66a7fca;

    bytes32 internal constant RELEASE_ID = keccak256("robin.rwa-user-vault.v1");
    uint128 internal constant MAX_INVENTORY = 1e18;
    uint8 internal constant MARKET_FEED_DECIMALS = 8;
    uint64 internal constant MARKET_VERSION = 1;
    uint64 internal constant MARKET_HEARTBEAT = 60 seconds;
    uint64 internal constant MAX_DEADLINE_DELAY = 5 minutes;
    uint64 internal constant SEQUENCER_GRACE_PERIOD = 5 minutes;
    uint64 internal constant POLICY_VERSION = 1;
    uint16 internal constant MAX_SLIPPAGE_BPS = 100;
    uint256 internal constant MAX_SPOT_NOTIONAL = 25e6;
    uint256 internal constant MAX_PAIR_GROSS = 50e6;
    uint256 internal constant TURNOVER_LIMIT = 50e6;
    uint64 internal constant TURNOVER_WINDOW = 1 days;

    struct ReleaseConfig {
        address timelock;
        bytes32 timelockCodeHash;
        uint256 timelockMinDelay;
        address guardian;
        address publisher1;
        address publisher2;
        address publisher3;
        address marketFeed;
        bytes32 marketFeedCodeHash;
        bytes32 deploymentSalt;
    }

    struct Deployment {
        QuorumSequencerFeed sequencerFeed;
        MainnetExecutionRegistry registry;
        RwaUserVaultFactoryV1 factory;
    }

    event ReleaseDeployed(
        address indexed registry,
        address indexed factory,
        address indexed sequencerFeed,
        bytes32 policyDigest,
        bytes32 factoryCodeHash
    );

    error UnexpectedChain(uint256 chainId);
    error InvalidConfiguration();
    error CodeHashMismatch(address account, bytes32 expected, bytes32 actual);
    error DeploymentNotDisabled();

    function run() external returns (Deployment memory deployment) {
        ReleaseConfig memory config = ReleaseConfig({
            timelock: vm.envAddress("USER_VAULT_TIMELOCK"),
            timelockCodeHash: vm.envBytes32("USER_VAULT_TIMELOCK_CODEHASH"),
            timelockMinDelay: vm.envUint("USER_VAULT_TIMELOCK_MIN_DELAY"),
            guardian: vm.envAddress("USER_VAULT_GUARDIAN"),
            publisher1: vm.envAddress("SEQUENCER_PUBLISHER_1"),
            publisher2: vm.envAddress("SEQUENCER_PUBLISHER_2"),
            publisher3: vm.envAddress("SEQUENCER_PUBLISHER_3"),
            marketFeed: vm.envAddress("AAPL_REFERENCE_FEED"),
            marketFeedCodeHash: vm.envBytes32("AAPL_REFERENCE_FEED_CODEHASH"),
            deploymentSalt: vm.envBytes32("USER_VAULT_RELEASE_SALT")
        });

        vm.startBroadcast();
        deployment = deploy(config);
        vm.stopBroadcast();

        bytes memory approval = approvalCalldata(deployment);
        console2.log("Sequencer feed", address(deployment.sequencerFeed));
        console2.log("Execution registry", address(deployment.registry));
        console2.log("User vault factory", address(deployment.factory));
        console2.log("Governance target", address(deployment.registry));
        console2.log("Factory code hash");
        console2.logBytes32(address(deployment.factory).codehash);
        console2.log("Timelock approval calldata");
        console2.logBytes(approval);
        console2.log("Execution", "halted");
        console2.log("Factory", "unapproved");
    }

    function deploy(ReleaseConfig memory config) public returns (Deployment memory deployment) {
        _validate(config);

        deployment.sequencerFeed = new QuorumSequencerFeed{
            salt: _salt(config.deploymentSalt, "SEQUENCER")
        }(
            config.publisher1, config.publisher2, config.publisher3
        );

        deployment.registry = new MainnetExecutionRegistry{
            salt: _salt(config.deploymentSalt, "REGISTRY")
        }(
            config.timelock, config.guardian
        );

        IRwaUserVaultFactoryV1.Policy memory policy = _policy(config, deployment.sequencerFeed);
        deployment.factory = new RwaUserVaultFactoryV1{
            salt: _salt(config.deploymentSalt, "FACTORY")
        }(
            deployment.registry, policy
        );

        if (
            deployment.registry.globalMode() != IMainnetExecutionRegistry.Mode.Halted
                || deployment.registry.isFactoryApproved(address(deployment.factory))
        ) revert DeploymentNotDisabled();

        emit ReleaseDeployed(
            address(deployment.registry),
            address(deployment.factory),
            address(deployment.sequencerFeed),
            deployment.factory.policyDigest(),
            address(deployment.factory).codehash
        );
    }

    function approvalCalldata(Deployment memory deployment) public view returns (bytes memory) {
        if (
            address(deployment.registry).code.length == 0
                || address(deployment.factory).code.length == 0
                || deployment.factory.registry() != address(deployment.registry)
                || deployment.registry.globalMode() != IMainnetExecutionRegistry.Mode.Halted
                || deployment.registry.isFactoryApproved(address(deployment.factory))
        ) revert DeploymentNotDisabled();

        return abi.encodeCall(
            MainnetExecutionRegistry.approveFactory,
            (address(deployment.factory), address(deployment.factory).codehash)
        );
    }

    function releaseDigest(ReleaseConfig memory config) public pure returns (bytes32) {
        return keccak256(abi.encode(RELEASE_ID, config));
    }

    function _policy(ReleaseConfig memory config, QuorumSequencerFeed sequencerFeed)
        internal
        view
        returns (IRwaUserVaultFactoryV1.Policy memory)
    {
        return IRwaUserVaultFactoryV1.Policy({
            settlementAsset: USDG,
            stockToken: AAPL,
            marketFeed: config.marketFeed,
            sequencerFeed: address(sequencerFeed),
            router: UNIVERSAL_ROUTER,
            permit2: PERMIT2,
            poolKey: PoolKey({
                currency0: USDG, currency1: AAPL, fee: 3000, tickSpacing: 60, hooks: address(0)
            }),
            settlementAssetCodeHash: _expectedCodeHash(USDG, config),
            stockTokenCodeHash: _expectedCodeHash(AAPL, config),
            marketFeedCodeHash: config.marketFeedCodeHash,
            sequencerFeedCodeHash: address(sequencerFeed).codehash,
            routerCodeHash: _expectedCodeHash(UNIVERSAL_ROUTER, config),
            permit2CodeHash: _expectedCodeHash(PERMIT2, config),
            maxInventory: MAX_INVENTORY,
            marketVersion: MARKET_VERSION,
            heartbeat: MARKET_HEARTBEAT,
            maxDeadlineDelay: MAX_DEADLINE_DELAY,
            sequencerGracePeriod: SEQUENCER_GRACE_PERIOD,
            policyVersion: POLICY_VERSION,
            maxSlippageBps: MAX_SLIPPAGE_BPS,
            maxSpotNotional: MAX_SPOT_NOTIONAL,
            maxPairGross: MAX_PAIR_GROSS,
            turnoverLimit: TURNOVER_LIMIT,
            turnoverWindow: TURNOVER_WINDOW
        });
    }

    function _validate(ReleaseConfig memory config) internal view {
        if (block.chainid != CHAIN_ID) revert UnexpectedChain(block.chainid);
        if (
            config.deploymentSalt == bytes32(0) || config.timelock == address(0)
                || config.guardian == address(0) || config.publisher1 == address(0)
                || config.publisher2 == address(0) || config.publisher3 == address(0)
                || config.marketFeed == address(0) || config.timelockCodeHash == bytes32(0)
                || config.marketFeedCodeHash == bytes32(0) || config.timelockMinDelay == 0
        ) revert InvalidConfiguration();
        if (
            config.timelock == config.guardian || config.publisher1 == config.publisher2
                || config.publisher1 == config.publisher3 || config.publisher2 == config.publisher3
                || _hasRoleOverlap(config) || _usesDependencyAsRole(config)
        ) revert InvalidConfiguration();

        _requireCodeHash(config.timelock, config.timelockCodeHash);
        if (IReleaseTimelock(config.timelock).getMinDelay() != config.timelockMinDelay) {
            revert InvalidConfiguration();
        }
        _requireCodeHash(USDG, _expectedCodeHash(USDG, config));
        _requireCodeHash(AAPL, _expectedCodeHash(AAPL, config));
        _requireCodeHash(UNIVERSAL_ROUTER, _expectedCodeHash(UNIVERSAL_ROUTER, config));
        _requireCodeHash(PERMIT2, _expectedCodeHash(PERMIT2, config));
        _requireCodeHash(config.marketFeed, config.marketFeedCodeHash);
        if (IChainlinkFeed(config.marketFeed).decimals() != MARKET_FEED_DECIMALS) {
            revert InvalidConfiguration();
        }
    }

    function _expectedCodeHash(address account, ReleaseConfig memory)
        internal
        view
        virtual
        returns (bytes32)
    {
        if (account == USDG) return USDG_CODE_HASH;
        if (account == AAPL) return AAPL_CODE_HASH;
        if (account == UNIVERSAL_ROUTER) return ROUTER_CODE_HASH;
        if (account == PERMIT2) return PERMIT2_CODE_HASH;
        revert InvalidConfiguration();
    }

    function _requireCodeHash(address account, bytes32 expected) private view {
        bytes32 actual = account.codehash;
        if (account.code.length == 0 || actual != expected) {
            revert CodeHashMismatch(account, expected, actual);
        }
    }

    function _hasRoleOverlap(ReleaseConfig memory config) private pure returns (bool) {
        address[5] memory roles = [
            config.timelock,
            config.guardian,
            config.publisher1,
            config.publisher2,
            config.publisher3
        ];
        for (uint256 i; i < roles.length; ++i) {
            if (roles[i] == config.marketFeed) return true;
            for (uint256 j = i + 1; j < roles.length; ++j) {
                if (roles[i] == roles[j]) return true;
            }
        }
        return false;
    }

    function _usesDependencyAsRole(ReleaseConfig memory config) private pure returns (bool) {
        address[6] memory accounts = [
            config.timelock,
            config.guardian,
            config.publisher1,
            config.publisher2,
            config.publisher3,
            config.marketFeed
        ];
        for (uint256 i; i < accounts.length; ++i) {
            if (
                accounts[i] == USDG || accounts[i] == AAPL || accounts[i] == UNIVERSAL_ROUTER
                    || accounts[i] == PERMIT2
            ) return true;
        }
        return false;
    }

    function _salt(bytes32 deploymentSalt, bytes32 component) private pure returns (bytes32) {
        return keccak256(abi.encode(RELEASE_ID, deploymentSalt, component));
    }
}
