// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { ERC20 } from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import { MainnetExecutionRegistry } from "../src/MainnetExecutionRegistry.sol";
import { MandateRiskManagerV1 } from "../src/MandateRiskManagerV1.sol";
import { QuorumAaplReferenceFeed } from "../src/QuorumAaplReferenceFeed.sol";
import { QuorumSequencerFeed } from "../src/QuorumSequencerFeed.sol";
import { RwaUserStrategyVaultV1 } from "../src/RwaUserStrategyVaultV1.sol";
import { RwaUserVaultFactoryV1 } from "../src/RwaUserVaultFactoryV1.sol";
import { UniswapV4SpotAdapter } from "../src/UniswapV4SpotAdapter.sol";
import { IMainnetExecutionRegistry } from "../src/interfaces/IMainnetExecutionRegistry.sol";
import { ISpotExecution } from "../src/interfaces/ISpotExecution.sol";
import {
    IPermit2AllowanceTransfer,
    IUniversalRouter,
    PoolKey
} from "../src/interfaces/IUniswapV4.sol";
import { IRwaUserVaultFactoryV1 } from "../src/interfaces/IRwaUserVaultFactoryV1.sol";

contract UserFactoryAdmin { }

contract UserSmartAccount { }

contract CanonicalUsdgMock is ERC20 {
    constructor() ERC20("Global Dollar", "USDG") { }

    function decimals() public pure override returns (uint8) {
        return 6;
    }

    function mint(address to, uint256 amount) external {
        _mint(to, amount);
    }
}

contract CanonicalAaplMock is ERC20 {
    constructor() ERC20("Apple Stock Token", "AAPL") { }

    function uiMultiplier() external pure returns (uint256) {
        return 1e18;
    }

    function newUIMultiplier() external pure returns (uint256) {
        return 1e18;
    }

    function effectiveAt() external pure returns (uint256) {
        return 0;
    }

    function oraclePaused() external pure returns (bool) {
        return false;
    }
}

contract CanonicalUserRouter is IUniversalRouter {
    function execute(bytes calldata, bytes[] calldata, uint256) external { }
}

contract CanonicalUserPermit2 is IPermit2AllowanceTransfer {
    function approve(address, address, uint160, uint48) external { }
}

contract RwaUserVaultFactoryV1Test is Test {
    address private constant USDG = 0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168;
    address private constant AAPL = 0xaF3D76f1834A1d425780943C99Ea8A608f8a93f9;
    address private constant ROUTER = 0x8876789976dEcBfCbBbe364623C63652db8C0904;
    address private constant PERMIT2 = 0x000000000022D473030F116dDEE9F6B43aC78BA3;

    UserFactoryAdmin private admin;
    MainnetExecutionRegistry private registry;
    QuorumSequencerFeed private sequencer;
    QuorumAaplReferenceFeed private marketFeed;
    RwaUserVaultFactoryV1 private factory;

    address private guardian = makeAddr("guardian");
    address private publisher1 = makeAddr("publisher-1");
    address private publisher2 = makeAddr("publisher-2");
    address private publisher3 = makeAddr("publisher-3");
    address private pricePublisher1 = makeAddr("price-publisher-1");
    address private pricePublisher2 = makeAddr("price-publisher-2");
    address private pricePublisher3 = makeAddr("price-publisher-3");
    address private owner = makeAddr("owner");

    function setUp() public {
        vm.chainId(4663);
        vm.warp(10_000);
        _installCanonicalContracts();

        admin = new UserFactoryAdmin();
        registry = new MainnetExecutionRegistry(address(admin), guardian);
        sequencer = new QuorumSequencerFeed(publisher1, publisher2, publisher3);
        marketFeed = new QuorumAaplReferenceFeed(pricePublisher1, pricePublisher2, pricePublisher3);
        vm.prank(pricePublisher1);
        marketFeed.report(1, 1, 100e8, uint64(block.timestamp - 1), 1);
        vm.prank(pricePublisher2);
        marketFeed.report(1, 1, 100e8, uint64(block.timestamp - 1), 1);
        factory = new RwaUserVaultFactoryV1(registry, _policy());

        vm.prank(address(admin));
        registry.approveFactory(address(factory), address(factory).codehash);
    }

    function testPermissionlessDeploymentIsDeterministicIdempotentAndIsolated() public {
        IRwaUserVaultFactoryV1.Graph memory predicted = factory.predictGraph(owner);
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);

        assertEq(graph.vault, predicted.vault);
        assertEq(graph.riskManager, predicted.riskManager);
        assertEq(graph.spotAdapter, predicted.spotAdapter);
        assertEq(registry.ownerOfVault(graph.vault), owner);
        assertEq(registry.factoryOfVault(graph.vault), address(factory));

        RwaUserStrategyVaultV1 vault = RwaUserStrategyVaultV1(graph.vault);
        MandateRiskManagerV1 risk = MandateRiskManagerV1(graph.riskManager);
        UniswapV4SpotAdapter adapter = UniswapV4SpotAdapter(graph.spotAdapter);
        assertEq(vault.owner(), owner);
        assertEq(address(vault.registry()), address(registry));
        assertEq(vault.agent(), address(0));
        assertFalse(vault.agentEnabled());
        assertEq(risk.treasury(), owner);
        assertEq(risk.executor(), graph.vault);
        assertEq(risk.grossNotionalLimit(), 25e6);
        assertEq(risk.turnoverLimit(), 50e6);
        assertEq(uint8(risk.mode()), uint8(MandateRiskManagerV1.Mode.Halted));
        assertTrue(risk.isMarket(AAPL));
        assertEq(adapter.vault(), graph.vault);
        assertEq(adapter.marketRoute(AAPL).poolKey.currency0, USDG);
        assertEq(adapter.marketRoute(AAPL).poolKey.currency1, AAPL);

        IRwaUserVaultFactoryV1.Graph memory repeated = factory.deploy(owner);
        assertEq(repeated.vault, graph.vault);

        UserSmartAccount smartOwner = new UserSmartAccount();
        IRwaUserVaultFactoryV1.Graph memory second = factory.deploy(address(smartOwner));
        assertNotEq(second.vault, graph.vault);
        assertEq(RwaUserStrategyVaultV1(second.vault).owner(), address(smartOwner));
    }

    function testGuardianCanOnlyRestrictGlobalMode() public {
        vm.prank(address(admin));
        registry.setGlobalMode(IMainnetExecutionRegistry.Mode.Active);

        vm.prank(guardian);
        registry.restrictGlobalMode(IMainnetExecutionRegistry.Mode.ReduceOnly);
        assertEq(uint8(registry.globalMode()), uint8(IMainnetExecutionRegistry.Mode.ReduceOnly));

        vm.prank(guardian);
        vm.expectRevert(MainnetExecutionRegistry.InvalidModeTransition.selector);
        registry.restrictGlobalMode(IMainnetExecutionRegistry.Mode.Active);

        vm.prank(guardian);
        registry.restrictGlobalMode(IMainnetExecutionRegistry.Mode.Halted);
        vm.prank(guardian);
        vm.expectRevert(MainnetExecutionRegistry.NotConfigAdmin.selector);
        registry.setGlobalMode(IMainnetExecutionRegistry.Mode.Active);
    }

    function testNewGraphInheritsActiveGlobalMode() public {
        vm.prank(address(admin));
        registry.setGlobalMode(IMainnetExecutionRegistry.Mode.Active);

        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        MandateRiskManagerV1 risk = MandateRiskManagerV1(graph.riskManager);

        assertEq(uint8(risk.mode()), uint8(MandateRiskManagerV1.Mode.Active));
    }

    function testNewGraphInheritsRestrictedGlobalModes() public {
        vm.prank(address(admin));
        registry.setGlobalMode(IMainnetExecutionRegistry.Mode.Active);
        vm.prank(guardian);
        registry.restrictGlobalMode(IMainnetExecutionRegistry.Mode.ReduceOnly);

        IRwaUserVaultFactoryV1.Graph memory reducing = factory.deploy(owner);
        assertEq(
            uint8(MandateRiskManagerV1(reducing.riskManager).mode()),
            uint8(MandateRiskManagerV1.Mode.ReduceOnly)
        );

        vm.prank(guardian);
        registry.restrictGlobalMode(IMainnetExecutionRegistry.Mode.Halted);
        IRwaUserVaultFactoryV1.Graph memory halted = factory.deploy(makeAddr("halted-owner"));
        assertEq(
            uint8(MandateRiskManagerV1(halted.riskManager).mode()),
            uint8(MandateRiskManagerV1.Mode.Halted)
        );
    }

    function testGuardianAndOwnerCanImmediatelyRestrictOnlyTheirVault() public {
        vm.prank(address(admin));
        registry.setGlobalMode(IMainnetExecutionRegistry.Mode.Active);
        IRwaUserVaultFactoryV1.Graph memory first = factory.deploy(owner);
        address secondOwner = makeAddr("second-owner");
        IRwaUserVaultFactoryV1.Graph memory second = factory.deploy(secondOwner);

        vm.prank(guardian);
        registry.restrictVaultMode(first.vault, IMainnetExecutionRegistry.Mode.ReduceOnly);
        assertEq(
            uint8(MandateRiskManagerV1(first.riskManager).mode()),
            uint8(MandateRiskManagerV1.Mode.ReduceOnly)
        );
        assertEq(
            uint8(MandateRiskManagerV1(second.riskManager).mode()),
            uint8(MandateRiskManagerV1.Mode.Active)
        );

        vm.prank(owner);
        vm.expectRevert(MainnetExecutionRegistry.NotRestrictor.selector);
        registry.restrictVaultMode(second.vault, IMainnetExecutionRegistry.Mode.Halted);
        vm.prank(secondOwner);
        registry.restrictVaultMode(second.vault, IMainnetExecutionRegistry.Mode.Halted);
        assertEq(
            uint8(MandateRiskManagerV1(second.riskManager).mode()),
            uint8(MandateRiskManagerV1.Mode.Halted)
        );
    }

    function testVaultRestrictionCannotExpandAuthorityOrCrossAccounts() public {
        vm.prank(address(admin));
        registry.setGlobalMode(IMainnetExecutionRegistry.Mode.Active);
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);

        vm.prank(makeAddr("attacker"));
        vm.expectRevert(MainnetExecutionRegistry.NotRestrictor.selector);
        registry.restrictVaultMode(graph.vault, IMainnetExecutionRegistry.Mode.Halted);

        vm.prank(owner);
        vm.expectRevert(MainnetExecutionRegistry.InvalidModeTransition.selector);
        registry.restrictVaultMode(graph.vault, IMainnetExecutionRegistry.Mode.Active);

        vm.prank(owner);
        registry.restrictVaultMode(graph.vault, IMainnetExecutionRegistry.Mode.Halted);
        vm.prank(guardian);
        vm.expectRevert(MainnetExecutionRegistry.InvalidModeTransition.selector);
        registry.restrictVaultMode(graph.vault, IMainnetExecutionRegistry.Mode.ReduceOnly);
    }

    function testVaultEnforcesGlobalModeBeforeExecution() public {
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        RwaUserStrategyVaultV1 vault = RwaUserStrategyVaultV1(graph.vault);
        address agent = makeAddr("agent");
        vm.startPrank(address(admin));
        registry.setVaultAgent(graph.vault, agent);
        registry.setVaultMode(graph.vault, IMainnetExecutionRegistry.Mode.Active);
        vm.stopPrank();
        vm.prank(owner);
        vault.enableAgent();

        ISpotExecution.SpotIntent memory intent = ISpotExecution.SpotIntent({
            id: keccak256("global-mode"),
            stockToken: AAPL,
            side: ISpotExecution.Side.BuySpot,
            amountIn: 1e6,
            minAmountOut: 1e16,
            expectedUIMultiplier: 1e18,
            minOracleRoundId: 1,
            deadline: uint64(block.timestamp + 1 minutes),
            configVersion: 1
        });

        vm.prank(agent);
        vm.expectRevert(RwaUserStrategyVaultV1.GlobalHalt.selector);
        vault.executeSpot(intent);

        vm.prank(address(admin));
        registry.setGlobalMode(IMainnetExecutionRegistry.Mode.ReduceOnly);
        vm.prank(agent);
        vm.expectRevert(RwaUserStrategyVaultV1.GlobalReduceOnly.selector);
        vault.executeSpot(intent);

        vm.prank(address(admin));
        registry.setVaultMode(graph.vault, IMainnetExecutionRegistry.Mode.Halted);
        vm.prank(address(admin));
        registry.setGlobalMode(IMainnetExecutionRegistry.Mode.Active);
        vm.prank(agent);
        vm.expectRevert(MandateRiskManagerV1.Halted.selector);
        vault.executeSpot(intent);
    }

    function testVaultEnforcesOneEntryAndFullExitPerEpisode() public {
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        RwaUserStrategyVaultV1 vault = RwaUserStrategyVaultV1(graph.vault);
        MandateRiskManagerV1 risk = MandateRiskManagerV1(graph.riskManager);
        address agent = makeAddr("episode-agent");
        vm.startPrank(address(admin));
        registry.setGlobalMode(IMainnetExecutionRegistry.Mode.Active);
        registry.setVaultAgent(graph.vault, agent);
        registry.setVaultMode(graph.vault, IMainnetExecutionRegistry.Mode.Active);
        vm.stopPrank();
        vm.prank(owner);
        vault.enableAgent();

        vm.mockCall(
            address(risk),
            abi.encodeWithSignature("inventory(address)", AAPL),
            abi.encode(uint256(1e18))
        );
        vm.prank(agent);
        vm.expectRevert(RwaUserStrategyVaultV1.EpisodeAlreadyActive.selector);
        vault.executeSpot(_intent(keccak256("duplicate-entry")));

        ISpotExecution.SpotIntent memory partialExit = _intent(keccak256("partial-exit"));
        partialExit.side = ISpotExecution.Side.SellSpot;
        partialExit.amountIn = 5e17;
        vm.prank(agent);
        vm.expectRevert(
            abi.encodeWithSelector(RwaUserStrategyVaultV1.FullExitRequired.selector, 1e18, 5e17)
        );
        vault.executeSpot(partialExit);
    }

    function testTimelockAgentReplacementCannotBypassOwnerRevoke() public {
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        RwaUserStrategyVaultV1 vault = RwaUserStrategyVaultV1(graph.vault);
        address firstAgent = makeAddr("first-agent");
        address replacement = makeAddr("replacement-agent");

        vm.prank(address(admin));
        registry.setVaultAgent(graph.vault, firstAgent);
        assertFalse(vault.agentEnabled());
        vm.prank(owner);
        vault.enableAgent();
        vm.prank(firstAgent);
        vault.anchorBatch(keccak256("first"), 1, 1);

        vm.prank(owner);
        vault.revokeAgent();
        vm.prank(address(admin));
        registry.setVaultAgent(graph.vault, replacement);
        assertEq(vault.agent(), replacement);
        assertFalse(vault.agentEnabled());

        vm.prank(replacement);
        vm.expectRevert(RwaUserStrategyVaultV1.AgentNotEnabled.selector);
        vault.executeSpot(_intent(keccak256("replacement-blocked")));

        vm.prank(owner);
        vault.enableAgent();
        vm.prank(replacement);
        vault.anchorBatch(keccak256("replacement"), 2, 1);
        assertTrue(vault.agentEnabled());
    }

    function testOwnerCanAtomicallyAuthorizeExecutionAgent() public {
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        RwaUserStrategyVaultV1 vault = RwaUserStrategyVaultV1(graph.vault);
        address executionAgent = makeAddr("execution-agent");

        vm.prank(makeAddr("attacker"));
        vm.expectRevert(RwaUserStrategyVaultV1.NotOwner.selector);
        vault.authorizeInitialAgent(executionAgent);

        vm.prank(owner);
        vault.authorizeInitialAgent(executionAgent);
        assertEq(vault.agent(), executionAgent);
        assertTrue(vault.agentEnabled());
        assertTrue(vault.initialAgentAuthorized());

        vm.prank(executionAgent);
        vault.anchorBatch(keccak256("owner-authorized"), 1, 1);

        vm.prank(owner);
        vault.revokeAgent();
        assertEq(vault.agent(), address(0));
        assertFalse(vault.agentEnabled());

        vm.prank(owner);
        vm.expectRevert(RwaUserStrategyVaultV1.InitialAgentAlreadyAuthorized.selector);
        vault.authorizeInitialAgent(makeAddr("replacement"));
    }

    function testOwnerCannotAuthorizeControlRolesAsAgent() public {
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        RwaUserStrategyVaultV1 vault = RwaUserStrategyVaultV1(graph.vault);
        address[4] memory forbidden = [owner, address(registry), address(admin), guardian];

        for (uint256 i; i < forbidden.length; ++i) {
            vm.prank(owner);
            vm.expectRevert(RwaUserStrategyVaultV1.InvalidAddress.selector);
            vault.authorizeInitialAgent(forbidden[i]);
        }
    }

    function testOwnerEmergencyHaltAtomicallyRevokesExecution() public {
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        RwaUserStrategyVaultV1 vault = RwaUserStrategyVaultV1(graph.vault);
        MandateRiskManagerV1 risk = MandateRiskManagerV1(graph.riskManager);
        address agent = makeAddr("agent");

        vm.startPrank(address(admin));
        registry.setVaultAgent(graph.vault, agent);
        registry.setVaultMode(graph.vault, IMainnetExecutionRegistry.Mode.Active);
        vm.stopPrank();
        vm.prank(owner);
        vault.enableAgent();

        vm.expectRevert(RwaUserStrategyVaultV1.NotOwner.selector);
        vault.emergencyHalt();
        vm.prank(address(admin));
        vm.expectRevert(MandateRiskManagerV1.NotExecutor.selector);
        risk.haltFromExecutor();

        vm.prank(owner);
        vault.emergencyHalt();
        assertEq(uint8(risk.mode()), uint8(MandateRiskManagerV1.Mode.Halted));
        assertEq(vault.agent(), address(0));
        assertFalse(vault.agentEnabled());

        vm.prank(agent);
        vm.expectRevert(RwaUserStrategyVaultV1.NotAgent.selector);
        vault.anchorBatch(keccak256("blocked"), 1, 1);
    }

    function testOwnerCanLowerCapsHaltAndRevokeButCannotRaiseCaps() public {
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        MandateRiskManagerV1 risk = MandateRiskManagerV1(graph.riskManager);
        RwaUserStrategyVaultV1 vault = RwaUserStrategyVaultV1(graph.vault);
        address agent = makeAddr("agent");

        vm.prank(address(admin));
        registry.setVaultAgent(graph.vault, agent);
        vm.prank(address(admin));
        registry.setVaultMode(graph.vault, IMainnetExecutionRegistry.Mode.Active);

        vm.prank(owner);
        risk.lowerLimits(10e6, 20e6);
        assertEq(risk.grossNotionalLimit(), 10e6);
        assertEq(risk.turnoverLimit(), 20e6);

        vm.prank(owner);
        vm.expectRevert(MandateRiskManagerV1.LimitsCanOnlyDecrease.selector);
        risk.lowerLimits(11e6, 20e6);

        vm.prank(owner);
        risk.restrictMode(MandateRiskManagerV1.Mode.Halted);
        vm.prank(owner);
        vault.revokeAgent();
        assertEq(vault.agent(), address(0));

        vm.prank(owner);
        vm.expectRevert(MandateRiskManagerV1.InvalidModeTransition.selector);
        risk.restrictMode(MandateRiskManagerV1.Mode.Active);
    }

    function testOwnerCannotEnableMissingAgent() public {
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        RwaUserStrategyVaultV1 vault = RwaUserStrategyVaultV1(graph.vault);

        vm.prank(owner);
        vm.expectRevert(RwaUserStrategyVaultV1.InvalidAddress.selector);
        vault.enableAgent();
    }

    function testFuzzOwnerCanLowerCapsWithinFixedCeilings(uint32 gross, uint32 turnover) public {
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        MandateRiskManagerV1 risk = MandateRiskManagerV1(graph.riskManager);
        gross = uint32(bound(gross, 1, 25e6));
        turnover = uint32(bound(turnover, 1, 50e6));

        vm.prank(owner);
        risk.lowerLimits(gross, turnover);
        assertEq(risk.grossNotionalLimit(), gross);
        assertEq(risk.turnoverLimit(), turnover);
    }

    function testOnlyFlatHaltedRevokedOwnerCanWithdraw() public {
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        RwaUserStrategyVaultV1 vault = RwaUserStrategyVaultV1(graph.vault);
        MandateRiskManagerV1 risk = MandateRiskManagerV1(graph.riskManager);
        address agent = makeAddr("agent");

        CanonicalUsdgMock(USDG).mint(owner, 25e6);
        vm.prank(owner);
        CanonicalUsdgMock(USDG).approve(graph.vault, 25e6);
        vm.prank(owner);
        vault.deposit(25e6);

        vm.prank(address(admin));
        registry.setVaultAgent(graph.vault, agent);
        vm.prank(address(admin));
        registry.setVaultMode(graph.vault, IMainnetExecutionRegistry.Mode.Active);

        vm.prank(owner);
        vm.expectRevert(RwaUserStrategyVaultV1.RecoveryRequiresHalt.selector);
        vault.withdrawSettlement(25e6);

        vm.prank(owner);
        risk.restrictMode(MandateRiskManagerV1.Mode.Halted);
        vm.prank(owner);
        vm.expectRevert(RwaUserStrategyVaultV1.AgentStillAuthorized.selector);
        vault.withdrawSettlement(25e6);

        vm.prank(owner);
        vault.revokeAgent();
        vm.prank(address(admin));
        vm.expectRevert(RwaUserStrategyVaultV1.NotOwner.selector);
        vault.withdrawSettlement(25e6);

        vm.prank(owner);
        vault.withdrawSettlement(25e6);
        assertEq(CanonicalUsdgMock(USDG).balanceOf(owner), 25e6);
    }

    function testPendingExecutionPreventsNormalWithdrawalButTerminalRecoveryRemains() public {
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        RwaUserStrategyVaultV1 vault = RwaUserStrategyVaultV1(graph.vault);
        MandateRiskManagerV1 risk = MandateRiskManagerV1(graph.riskManager);
        _reportHealthySequencer();

        vm.prank(address(admin));
        registry.setVaultMode(graph.vault, IMainnetExecutionRegistry.Mode.Active);
        vm.prank(address(admin));
        registry.setVaultAgent(graph.vault, makeAddr("recovery-agent"));
        vm.prank(owner);
        vault.enableAgent();
        ISpotExecution.SpotIntent memory intent = ISpotExecution.SpotIntent({
            id: keccak256("pending"),
            stockToken: AAPL,
            side: ISpotExecution.Side.BuySpot,
            amountIn: 1e6,
            minAmountOut: 1e16,
            expectedUIMultiplier: 1e18,
            minOracleRoundId: 1,
            deadline: uint64(block.timestamp + 1 minutes),
            configVersion: 1
        });
        vm.prank(graph.vault);
        risk.authorize(intent);

        vm.prank(owner);
        risk.restrictMode(MandateRiskManagerV1.Mode.Halted);
        vm.prank(owner);
        vm.expectRevert(RwaUserStrategyVaultV1.VaultNotFlat.selector);
        vault.withdrawSettlement(1);

        CanonicalUsdgMock(USDG).mint(graph.vault, 1e6);
        vm.prank(owner);
        vault.finalizeRecovery();
        assertEq(vault.agent(), address(0));
        assertFalse(vault.agentEnabled());
        vm.prank(owner);
        vault.recover(CanonicalUsdgMock(USDG), 1e6);
        assertEq(CanonicalUsdgMock(USDG).balanceOf(owner), 1e6);
    }

    function testPinnedFeedCodeChangeFailsClosed() public {
        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        MandateRiskManagerV1 risk = MandateRiskManagerV1(graph.riskManager);
        _reportHealthySequencer();
        vm.prank(address(admin));
        registry.setVaultMode(graph.vault, IMainnetExecutionRegistry.Mode.Active);

        bytes32 expected = address(marketFeed).codehash;
        vm.etch(address(marketFeed), hex"00");
        bytes32 actual = address(marketFeed).codehash;
        ISpotExecution.SpotIntent memory intent = ISpotExecution.SpotIntent({
            id: keccak256("changed-feed"),
            stockToken: AAPL,
            side: ISpotExecution.Side.BuySpot,
            amountIn: 1e6,
            minAmountOut: 1e16,
            expectedUIMultiplier: 1e18,
            minOracleRoundId: 1,
            deadline: uint64(block.timestamp + 1 minutes),
            configVersion: 1
        });
        vm.prank(graph.vault);
        vm.expectRevert(
            abi.encodeWithSelector(
                MandateRiskManagerV1.ExternalCodeChanged.selector,
                address(marketFeed),
                expected,
                actual
            )
        );
        risk.authorize(intent);
    }

    function testGuardianCanDisableButNotApproveFactory() public {
        vm.prank(guardian);
        registry.disableFactory(address(factory));
        assertFalse(registry.isFactoryApproved(address(factory)));

        vm.prank(guardian);
        vm.expectRevert(MainnetExecutionRegistry.NotConfigAdmin.selector);
        registry.approveFactory(address(factory), address(factory).codehash);

        vm.expectRevert(RwaUserVaultFactoryV1.FactoryNotApproved.selector);
        factory.deploy(owner);
    }

    function testRejectsNonCanonicalAaplRouteAtApproval() public {
        IRwaUserVaultFactoryV1.Policy memory wrong = _policy();
        wrong.poolKey.fee = 500;
        RwaUserVaultFactoryV1 wrongFactory = new RwaUserVaultFactoryV1(registry, wrong);

        vm.prank(address(admin));
        vm.expectRevert(MainnetExecutionRegistry.InvalidConfiguration.selector);
        registry.approveFactory(address(wrongFactory), address(wrongFactory).codehash);
    }

    function testRejectsNonMainnetDeployment() public {
        vm.chainId(1);
        vm.expectRevert(
            abi.encodeWithSelector(MainnetExecutionRegistry.UnsupportedChain.selector, 1)
        );
        new MainnetExecutionRegistry(address(admin), guardian);
    }

    function _policy() private view returns (IRwaUserVaultFactoryV1.Policy memory) {
        return IRwaUserVaultFactoryV1.Policy({
            settlementAsset: USDG,
            stockToken: AAPL,
            marketFeed: address(marketFeed),
            sequencerFeed: address(sequencer),
            router: ROUTER,
            permit2: PERMIT2,
            poolKey: PoolKey({
                currency0: USDG, currency1: AAPL, fee: 10_000, tickSpacing: 200, hooks: address(0)
            }),
            settlementAssetCodeHash: USDG.codehash,
            stockTokenCodeHash: AAPL.codehash,
            marketFeedCodeHash: address(marketFeed).codehash,
            sequencerFeedCodeHash: address(sequencer).codehash,
            routerCodeHash: ROUTER.codehash,
            permit2CodeHash: PERMIT2.codehash,
            maxInventory: 1e18,
            marketVersion: 1,
            heartbeat: 25 hours,
            maxDeadlineDelay: 5 minutes,
            sequencerGracePeriod: 5 minutes,
            policyVersion: 1,
            maxSlippageBps: 200,
            maxSpotNotional: 25e6,
            maxPairGross: 50e6,
            turnoverLimit: 50e6,
            turnoverWindow: 1 days
        });
    }

    function _installCanonicalContracts() private {
        CanonicalUsdgMock usdg = new CanonicalUsdgMock();
        CanonicalAaplMock aapl = new CanonicalAaplMock();
        CanonicalUserRouter router = new CanonicalUserRouter();
        CanonicalUserPermit2 permit2 = new CanonicalUserPermit2();
        vm.etch(USDG, address(usdg).code);
        vm.etch(AAPL, address(aapl).code);
        vm.etch(ROUTER, address(router).code);
        vm.etch(PERMIT2, address(permit2).code);
    }

    function _reportHealthySequencer() private {
        vm.prank(publisher1);
        sequencer.report(1, true, uint64(block.timestamp - 1 hours));
        vm.prank(publisher2);
        sequencer.report(1, true, uint64(block.timestamp - 1 hours));
    }

    function _intent(bytes32 id) private view returns (ISpotExecution.SpotIntent memory) {
        return ISpotExecution.SpotIntent({
            id: id,
            stockToken: AAPL,
            side: ISpotExecution.Side.BuySpot,
            amountIn: 1e6,
            minAmountOut: 1e16,
            expectedUIMultiplier: 1e18,
            minOracleRoundId: 1,
            deadline: uint64(block.timestamp + 1 minutes),
            configVersion: 1
        });
    }
}
