// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { IERC20Metadata } from "@openzeppelin/contracts/token/ERC20/extensions/IERC20Metadata.sol";
import { ReentrancyGuard } from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import { MandateRiskManagerV1 } from "./MandateRiskManagerV1.sol";
import { RwaUserStrategyVaultV1 } from "./RwaUserStrategyVaultV1.sol";
import { UniswapV4SpotAdapter } from "./UniswapV4SpotAdapter.sol";
import {
    UserRiskManagerDeployerV1,
    UserSpotAdapterDeployerV1,
    UserVaultDeployerV1
} from "./RwaUserGraphDeployersV1.sol";
import { IChainlinkFeed } from "./interfaces/IChainlinkFeed.sol";
import { IMainnetExecutionRegistry } from "./interfaces/IMainnetExecutionRegistry.sol";
import { IPermit2AllowanceTransfer, IUniversalRouter } from "./interfaces/IUniswapV4.sol";
import { IRwaUserVaultFactoryV1 } from "./interfaces/IRwaUserVaultFactoryV1.sol";

contract RwaUserVaultFactoryV1 is IRwaUserVaultFactoryV1, ReentrancyGuard {
    uint256 public constant CHAIN_ID = 4663;

    address public immutable override registry;
    bytes32 public immutable override policyDigest;
    UserRiskManagerDeployerV1 public immutable riskDeployer;
    UserSpotAdapterDeployerV1 public immutable adapterDeployer;
    UserVaultDeployerV1 public immutable vaultDeployer;

    Policy private vaultPolicy;
    mapping(address => Graph) private ownerGraphs;

    event UserGraphDeployed(
        address indexed owner,
        address indexed vault,
        address riskManager,
        address spotAdapter,
        bytes32 policyDigest,
        address indexed relayer
    );

    error UnsupportedChain(uint256 chainId);
    error InvalidAddress();
    error InvalidPolicy();
    error FactoryNotApproved();
    error DeploymentMismatch();

    constructor(IMainnetExecutionRegistry registry_, Policy memory policy_) {
        if (block.chainid != CHAIN_ID) revert UnsupportedChain(block.chainid);
        if (address(registry_).code.length == 0) revert InvalidAddress();
        _validatePolicy(policy_);
        registry = address(registry_);
        vaultPolicy = policy_;
        policyDigest = keccak256(abi.encode(policy_));
        riskDeployer = new UserRiskManagerDeployerV1();
        adapterDeployer = new UserSpotAdapterDeployerV1();
        vaultDeployer = new UserVaultDeployerV1();
    }

    function policy() external view override returns (Policy memory) {
        return vaultPolicy;
    }

    function graphForOwner(address owner) external view override returns (Graph memory) {
        return ownerGraphs[owner];
    }

    function deploy(address owner) external nonReentrant returns (Graph memory graph) {
        if (block.chainid != CHAIN_ID) revert UnsupportedChain(block.chainid);
        IMainnetExecutionRegistry registry_ = IMainnetExecutionRegistry(registry);
        if (!registry_.isFactoryApproved(address(this))) revert FactoryNotApproved();
        if (
            owner == address(0) || owner == registry || owner == registry_.configAdmin()
                || owner == registry_.guardian()
        ) revert InvalidAddress();

        graph = ownerGraphs[owner];
        if (graph.vault != address(0)) return graph;

        Graph memory predicted = predictGraph(owner);
        Policy memory policy_ = vaultPolicy;

        MandateRiskManagerV1 risk = riskDeployer.deploy(
            _salt(owner, "RISK"),
            IERC20Metadata(policy_.settlementAsset),
            IChainlinkFeed(policy_.sequencerFeed),
            registry,
            registry_.guardian(),
            owner,
            policy_.maxSpotNotional,
            policy_.turnoverLimit,
            policy_.turnoverWindow,
            policy_.maxDeadlineDelay,
            policy_.sequencerGracePeriod
        );
        UniswapV4SpotAdapter adapter = adapterDeployer.deploy(
            _salt(owner, "ADAPTER"),
            IERC20(policy_.settlementAsset),
            IUniversalRouter(policy_.router),
            IPermit2AllowanceTransfer(policy_.permit2),
            registry,
            policy_.routerCodeHash,
            policy_.permit2CodeHash
        );
        RwaUserStrategyVaultV1 vault = vaultDeployer.deploy(
            _salt(owner, "VAULT"), IERC20(policy_.settlementAsset), risk, adapter, registry_, owner
        );

        if (
            address(risk) != predicted.riskManager || address(adapter) != predicted.spotAdapter
                || address(vault) != predicted.vault
        ) revert DeploymentMismatch();

        risk.bindExecutor(address(vault));
        adapter.bindVault(address(vault));
        graph = Graph({
            riskManager: address(risk), spotAdapter: address(adapter), vault: address(vault)
        });
        ownerGraphs[owner] = graph;
        registry_.registerGraph(owner, graph.vault, graph.riskManager, graph.spotAdapter);

        emit UserGraphDeployed(
            owner, graph.vault, graph.riskManager, graph.spotAdapter, policyDigest, msg.sender
        );
    }

    function predictGraph(address owner) public view returns (Graph memory graph) {
        if (owner == address(0)) revert InvalidAddress();
        Policy memory policy_ = vaultPolicy;

        graph.riskManager = riskDeployer.predict(
            _salt(owner, "RISK"),
            IERC20Metadata(policy_.settlementAsset),
            IChainlinkFeed(policy_.sequencerFeed),
            registry,
            IMainnetExecutionRegistry(registry).guardian(),
            owner,
            policy_.maxSpotNotional,
            policy_.turnoverLimit,
            policy_.turnoverWindow,
            policy_.maxDeadlineDelay,
            policy_.sequencerGracePeriod
        );
        graph.spotAdapter = adapterDeployer.predict(
            _salt(owner, "ADAPTER"),
            IERC20(policy_.settlementAsset),
            IUniversalRouter(policy_.router),
            IPermit2AllowanceTransfer(policy_.permit2),
            registry,
            policy_.routerCodeHash,
            policy_.permit2CodeHash
        );
        graph.vault = vaultDeployer.predict(
            _salt(owner, "VAULT"),
            IERC20(policy_.settlementAsset),
            MandateRiskManagerV1(graph.riskManager),
            UniswapV4SpotAdapter(graph.spotAdapter),
            IMainnetExecutionRegistry(registry),
            owner
        );
    }

    function _salt(address owner, bytes32 component) private view returns (bytes32) {
        return keccak256(abi.encode(owner, policyDigest, component));
    }

    function _validatePolicy(Policy memory policy_) private view {
        if (
            policy_.settlementAsset.code.length == 0 || policy_.stockToken.code.length == 0
                || policy_.marketFeed.code.length == 0 || policy_.sequencerFeed.code.length == 0
                || policy_.router.code.length == 0 || policy_.permit2.code.length == 0
        ) revert InvalidAddress();
        if (
            policy_.settlementAsset.codehash != policy_.settlementAssetCodeHash
                || policy_.stockToken.codehash != policy_.stockTokenCodeHash
                || policy_.marketFeed.codehash != policy_.marketFeedCodeHash
                || policy_.sequencerFeed.codehash != policy_.sequencerFeedCodeHash
                || policy_.router.codehash != policy_.routerCodeHash
                || policy_.permit2.codehash != policy_.permit2CodeHash
        ) revert InvalidPolicy();
        if (
            policy_.poolKey.currency0 != policy_.settlementAsset
                || policy_.poolKey.currency1 != policy_.stockToken
                || policy_.poolKey.hooks != address(0) || policy_.poolKey.tickSpacing == 0
                || policy_.maxInventory == 0 || policy_.marketVersion == 0 || policy_.heartbeat == 0
                || policy_.maxDeadlineDelay == 0 || policy_.sequencerGracePeriod == 0
                || policy_.policyVersion == 0 || policy_.maxSpotNotional == 0
                || policy_.maxPairGross == 0 || policy_.turnoverLimit == 0
                || policy_.turnoverWindow == 0
        ) revert InvalidPolicy();
    }
}
