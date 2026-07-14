// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IERC20Metadata } from "@openzeppelin/contracts/token/ERC20/extensions/IERC20Metadata.sol";
import { MandateRiskManagerV1 } from "./MandateRiskManagerV1.sol";
import { RwaUserStrategyVaultV1 } from "./RwaUserStrategyVaultV1.sol";
import { UniswapV4SpotAdapter } from "./UniswapV4SpotAdapter.sol";
import { IChainlinkFeed } from "./interfaces/IChainlinkFeed.sol";
import { IMainnetExecutionRegistry } from "./interfaces/IMainnetExecutionRegistry.sol";
import { IQuorumAaplReferenceFeed } from "./interfaces/IQuorumAaplReferenceFeed.sol";
import { IQuorumSequencerFeed } from "./interfaces/IQuorumSequencerFeed.sol";
import { IRwaUserVaultFactoryV1 } from "./interfaces/IRwaUserVaultFactoryV1.sol";

contract MainnetExecutionRegistry is IMainnetExecutionRegistry {
    uint256 public constant CHAIN_ID = 4663;
    uint256 public constant MAX_SPOT_NOTIONAL = 25e6;
    uint256 public constant MAX_PAIR_GROSS = 50e6;
    uint256 public constant MAX_DAILY_TURNOVER = 50e6;
    uint64 public constant TURNOVER_WINDOW = 1 days;
    uint64 public constant AAPL_SOURCE_MAX_AGE = 25 hours;
    bytes32 public constant AAPL_USDG_POOL_ID =
        0xda4116b5894ee7479e64eae9276e1a2944ef0e5ce863a299d296a15618deee01;

    address public constant USDG = 0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168;
    address public constant AAPL = 0xaF3D76f1834A1d425780943C99Ea8A608f8a93f9;
    address public constant UNIVERSAL_ROUTER = 0x8876789976dEcBfCbBbe364623C63652db8C0904;
    address public constant PERMIT2 = 0x000000000022D473030F116dDEE9F6B43aC78BA3;

    struct FactoryApproval {
        bytes32 codeHash;
        bytes32 policyDigest;
        bool enabled;
    }

    address public immutable override configAdmin;
    address public immutable override guardian;
    Mode public override globalMode;

    mapping(address => FactoryApproval) public factoryApprovals;
    mapping(address => address) public ownerOfVault;
    mapping(address => address) public factoryOfVault;
    mapping(address => address) public riskManagerOfVault;
    mapping(address => address) public spotAdapterOfVault;

    event FactoryApproved(
        address indexed factory, bytes32 indexed codeHash, bytes32 indexed policyDigest
    );
    event FactoryDisabled(address indexed factory, address indexed caller);
    event GraphRegistered(
        address indexed factory,
        address indexed owner,
        address indexed vault,
        address riskManager,
        address spotAdapter,
        bytes32 policyDigest
    );
    event GlobalModeSet(Mode mode, address indexed caller);
    event VaultModeSet(address indexed vault, Mode mode);
    event VaultAgentSet(address indexed vault, address indexed agent);

    error UnsupportedChain(uint256 chainId);
    error NotConfigAdmin();
    error NotRestrictor();
    error InvalidAddress();
    error InvalidConfiguration();
    error InvalidModeTransition();
    error FactoryNotApproved(address factory);
    error FactoryCodeChanged(address factory, bytes32 expected, bytes32 actual);
    error VaultAlreadyRegistered(address vault);
    error UnknownVault(address vault);
    error InvalidGraph();

    modifier onlyConfigAdmin() {
        if (msg.sender != configAdmin) revert NotConfigAdmin();
        _;
    }

    constructor(address configAdmin_, address guardian_) {
        if (block.chainid != CHAIN_ID) revert UnsupportedChain(block.chainid);
        if (configAdmin_.code.length == 0 || guardian_ == address(0) || configAdmin_ == guardian_) {
            revert InvalidAddress();
        }
        configAdmin = configAdmin_;
        guardian = guardian_;
        globalMode = Mode.Halted;
    }

    function approveFactory(address factory, bytes32 expectedCodeHash) external onlyConfigAdmin {
        if (factory.code.length == 0 || expectedCodeHash == bytes32(0)) revert InvalidAddress();
        bytes32 actual = factory.codehash;
        if (actual != expectedCodeHash) {
            revert FactoryCodeChanged(factory, expectedCodeHash, actual);
        }

        IRwaUserVaultFactoryV1 candidate = IRwaUserVaultFactoryV1(factory);
        if (candidate.registry() != address(this)) revert InvalidConfiguration();
        IRwaUserVaultFactoryV1.Policy memory factoryPolicy = candidate.policy();
        bytes32 digest = keccak256(abi.encode(factoryPolicy));
        if (candidate.policyDigest() != digest) revert InvalidConfiguration();
        _validatePolicy(factoryPolicy);

        factoryApprovals[factory] =
            FactoryApproval({ codeHash: expectedCodeHash, policyDigest: digest, enabled: true });
        emit FactoryApproved(factory, expectedCodeHash, digest);
    }

    function disableFactory(address factory) external {
        if (msg.sender != configAdmin && msg.sender != guardian) revert NotRestrictor();
        FactoryApproval storage approval = factoryApprovals[factory];
        if (!approval.enabled) revert FactoryNotApproved(factory);
        approval.enabled = false;
        emit FactoryDisabled(factory, msg.sender);
    }

    function isFactoryApproved(address factory) public view override returns (bool) {
        FactoryApproval memory approval = factoryApprovals[factory];
        return approval.enabled && factory.codehash == approval.codeHash;
    }

    function isRegisteredVault(address vault) public view override returns (bool) {
        return ownerOfVault[vault] != address(0);
    }

    function registerGraph(address owner, address vault, address riskManager, address spotAdapter)
        external
        override
    {
        FactoryApproval memory approval = factoryApprovals[msg.sender];
        if (!approval.enabled) revert FactoryNotApproved(msg.sender);
        bytes32 actualFactoryHash = msg.sender.codehash;
        if (actualFactoryHash != approval.codeHash) {
            revert FactoryCodeChanged(msg.sender, approval.codeHash, actualFactoryHash);
        }
        if (owner == address(0) || vault == address(0)) revert InvalidAddress();
        if (isRegisteredVault(vault)) revert VaultAlreadyRegistered(vault);

        IRwaUserVaultFactoryV1 factory = IRwaUserVaultFactoryV1(msg.sender);
        IRwaUserVaultFactoryV1.Policy memory factoryPolicy = factory.policy();
        IRwaUserVaultFactoryV1.Graph memory graph = factory.graphForOwner(owner);
        if (
            factory.policyDigest() != approval.policyDigest
                || keccak256(abi.encode(factoryPolicy)) != approval.policyDigest
                || graph.vault != vault || graph.riskManager != riskManager
                || graph.spotAdapter != spotAdapter
        ) revert InvalidGraph();
        _validatePolicy(factoryPolicy);
        _validateGraph(owner, vault, riskManager, spotAdapter, factoryPolicy);

        ownerOfVault[vault] = owner;
        factoryOfVault[vault] = msg.sender;
        riskManagerOfVault[vault] = riskManager;
        spotAdapterOfVault[vault] = spotAdapter;

        MandateRiskManagerV1(riskManager)
            .setMarket(
                factoryPolicy.stockToken,
                IChainlinkFeed(factoryPolicy.marketFeed),
                uint128(factoryPolicy.maxSpotNotional),
                factoryPolicy.maxInventory,
                factoryPolicy.heartbeat,
                factoryPolicy.marketVersion,
                factoryPolicy.maxSlippageBps,
                true,
                true
            );
        UniswapV4SpotAdapter(spotAdapter)
            .setMarket(
                factoryPolicy.stockToken,
                factoryPolicy.poolKey,
                factoryPolicy.marketVersion,
                true,
                true
            );
        MandateRiskManagerV1.Mode riskMode = MandateRiskManagerV1.Mode(uint8(globalMode));
        MandateRiskManagerV1(riskManager).setMode(riskMode);

        emit GraphRegistered(
            msg.sender, owner, vault, riskManager, spotAdapter, approval.policyDigest
        );
        emit VaultModeSet(vault, globalMode);
    }

    function setGlobalMode(Mode mode) external onlyConfigAdmin {
        globalMode = mode;
        emit GlobalModeSet(mode, msg.sender);
    }

    function restrictGlobalMode(Mode mode) external {
        if (msg.sender != guardian) revert NotRestrictor();
        if (mode == Mode.Active || uint8(mode) < uint8(globalMode)) {
            revert InvalidModeTransition();
        }
        globalMode = mode;
        emit GlobalModeSet(mode, msg.sender);
    }

    function setVaultMode(address vault, Mode mode) external onlyConfigAdmin {
        address riskManager = riskManagerOfVault[vault];
        if (riskManager == address(0)) revert UnknownVault(vault);
        MandateRiskManagerV1(riskManager).setMode(MandateRiskManagerV1.Mode(uint8(mode)));
        emit VaultModeSet(vault, mode);
    }

    function restrictVaultMode(address vault, Mode mode) external override {
        address owner = ownerOfVault[vault];
        if (msg.sender != guardian && msg.sender != owner) revert NotRestrictor();
        address riskManager = riskManagerOfVault[vault];
        if (riskManager == address(0)) revert UnknownVault(vault);
        MandateRiskManagerV1.Mode current = MandateRiskManagerV1(riskManager).mode();
        if (mode == Mode.Active || uint8(mode) < uint8(current)) {
            revert InvalidModeTransition();
        }
        MandateRiskManagerV1(riskManager).setMode(MandateRiskManagerV1.Mode(uint8(mode)));
        emit VaultModeSet(vault, mode);
    }

    function setVaultAgent(address vault, address agent) external onlyConfigAdmin {
        address owner = ownerOfVault[vault];
        if (owner == address(0)) revert UnknownVault(vault);
        if (
            agent == address(0) || agent == owner || agent == address(this) || agent == configAdmin
                || agent == guardian
        ) revert InvalidAddress();
        RwaUserStrategyVaultV1(vault).setAgent(agent);
        emit VaultAgentSet(vault, agent);
    }

    function _validatePolicy(IRwaUserVaultFactoryV1.Policy memory factoryPolicy) private view {
        if (
            factoryPolicy.settlementAsset != USDG || factoryPolicy.stockToken != AAPL
                || factoryPolicy.router != UNIVERSAL_ROUTER || factoryPolicy.permit2 != PERMIT2
        ) revert InvalidConfiguration();
        if (
            factoryPolicy.poolKey.currency0 != USDG || factoryPolicy.poolKey.currency1 != AAPL
                || factoryPolicy.poolKey.fee != 10_000 || factoryPolicy.poolKey.tickSpacing != 200
                || factoryPolicy.poolKey.hooks != address(0)
                || keccak256(abi.encode(factoryPolicy.poolKey)) != AAPL_USDG_POOL_ID
        ) revert InvalidConfiguration();
        if (
            factoryPolicy.maxSpotNotional != MAX_SPOT_NOTIONAL
                || factoryPolicy.maxPairGross != MAX_PAIR_GROSS
                || factoryPolicy.turnoverLimit != MAX_DAILY_TURNOVER
                || factoryPolicy.turnoverWindow != TURNOVER_WINDOW
                || factoryPolicy.maxInventory == 0 || factoryPolicy.marketVersion == 0
                || factoryPolicy.heartbeat != AAPL_SOURCE_MAX_AGE
                || factoryPolicy.maxDeadlineDelay == 0 || factoryPolicy.sequencerGracePeriod == 0
                || factoryPolicy.policyVersion == 0 || factoryPolicy.maxSlippageBps > 200
        ) revert InvalidConfiguration();
        if (
            factoryPolicy.marketFeed.code.length == 0
                || factoryPolicy.sequencerFeed.code.length == 0
                || factoryPolicy.settlementAssetCodeHash == bytes32(0)
                || factoryPolicy.stockTokenCodeHash == bytes32(0)
                || factoryPolicy.marketFeedCodeHash == bytes32(0)
                || factoryPolicy.sequencerFeedCodeHash == bytes32(0)
                || factoryPolicy.routerCodeHash == bytes32(0)
                || factoryPolicy.permit2CodeHash == bytes32(0)
                || factoryPolicy.marketFeed.codehash != factoryPolicy.marketFeedCodeHash
                || factoryPolicy.sequencerFeed.codehash != factoryPolicy.sequencerFeedCodeHash
                || factoryPolicy.settlementAsset.codehash != factoryPolicy.settlementAssetCodeHash
                || factoryPolicy.stockToken.codehash != factoryPolicy.stockTokenCodeHash
                || factoryPolicy.router.codehash != factoryPolicy.routerCodeHash
                || factoryPolicy.permit2.codehash != factoryPolicy.permit2CodeHash
        ) revert InvalidConfiguration();
        if (IERC20Metadata(factoryPolicy.settlementAsset).decimals() != 6) {
            revert InvalidConfiguration();
        }
        IQuorumSequencerFeed sequencer = IQuorumSequencerFeed(factoryPolicy.sequencerFeed);
        if (
            sequencer.decimals() != 0 || sequencer.publisherCount() != 3 || sequencer.quorum() != 2
                || sequencer.maxAge() != 60 seconds
        ) revert InvalidConfiguration();
        IQuorumAaplReferenceFeed market = IQuorumAaplReferenceFeed(factoryPolicy.marketFeed);
        if (
            market.decimals() != 8 || market.publisherCount() != 3 || market.quorum() != 2
                || market.maxReportAge() != 60 seconds
                || market.maxSourceAge() != AAPL_SOURCE_MAX_AGE
                || market.publisher1() == market.publisher2()
                || market.publisher1() == market.publisher3()
                || market.publisher2() == market.publisher3()
        ) revert InvalidConfiguration();
    }

    function _validateGraph(
        address owner,
        address vaultAddress,
        address riskAddress,
        address adapterAddress,
        IRwaUserVaultFactoryV1.Policy memory factoryPolicy
    ) private view {
        if (
            vaultAddress.code.length == 0 || riskAddress.code.length == 0
                || adapterAddress.code.length == 0
        ) revert InvalidGraph();

        RwaUserStrategyVaultV1 vault = RwaUserStrategyVaultV1(vaultAddress);
        MandateRiskManagerV1 risk = MandateRiskManagerV1(riskAddress);
        UniswapV4SpotAdapter adapter = UniswapV4SpotAdapter(adapterAddress);
        if (
            address(vault.registry()) != address(this) || vault.owner() != owner
                || address(vault.settlementAsset()) != factoryPolicy.settlementAsset
                || address(vault.riskManager()) != riskAddress
                || address(vault.spotAdapter()) != adapterAddress || vault.agent() != address(0)
                || vault.agentEnabled() || risk.configAdmin() != address(this)
                || risk.guardian() != guardian || risk.treasury() != owner
                || risk.executor() != vaultAddress
                || address(risk.settlementAsset()) != factoryPolicy.settlementAsset
                || risk.settlementAssetCodeHash() != factoryPolicy.settlementAssetCodeHash
                || address(risk.sequencerFeed()) != factoryPolicy.sequencerFeed
                || risk.sequencerFeedCodeHash() != factoryPolicy.sequencerFeedCodeHash
                || risk.grossNotionalLimit() != factoryPolicy.maxSpotNotional
                || risk.turnoverLimit() != factoryPolicy.turnoverLimit
                || risk.turnoverWindow() != factoryPolicy.turnoverWindow
                || risk.maxDeadlineDelay() != factoryPolicy.maxDeadlineDelay
                || risk.sequencerGracePeriod() != factoryPolicy.sequencerGracePeriod
                || risk.maxActiveMarkets() != 1 || risk.mode() != MandateRiskManagerV1.Mode.Halted
                || address(adapter.settlementAsset()) != factoryPolicy.settlementAsset
                || address(adapter.router()) != factoryPolicy.router
                || address(adapter.permit2()) != factoryPolicy.permit2
                || adapter.configAdmin() != address(this) || adapter.vault() != vaultAddress
                || adapter.routerCodeHash() != factoryPolicy.routerCodeHash
                || adapter.permit2CodeHash() != factoryPolicy.permit2CodeHash
        ) revert InvalidGraph();
    }
}
