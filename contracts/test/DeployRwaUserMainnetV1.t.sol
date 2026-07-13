// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { ERC20 } from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import { DeployRwaUserMainnetV1 } from "../script/DeployRwaUserMainnetV1.s.sol";
import { MainnetExecutionRegistry } from "../src/MainnetExecutionRegistry.sol";
import { IMainnetExecutionRegistry } from "../src/interfaces/IMainnetExecutionRegistry.sol";
import { IChainlinkFeed } from "../src/interfaces/IChainlinkFeed.sol";
import { IPermit2AllowanceTransfer, IUniversalRouter } from "../src/interfaces/IUniswapV4.sol";
import { IRwaUserVaultFactoryV1 } from "../src/interfaces/IRwaUserVaultFactoryV1.sol";

contract ReleaseUsdgMock is ERC20 {
    constructor() ERC20("Global Dollar", "USDG") { }

    function decimals() public pure override returns (uint8) {
        return 6;
    }
}

contract ReleaseAaplMock is ERC20 {
    constructor() ERC20("Apple Stock Token", "AAPL") { }
}

contract ReleaseRouterMock is IUniversalRouter {
    function execute(bytes calldata, bytes[] calldata, uint256) external { }
}

contract ReleasePermit2Mock is IPermit2AllowanceTransfer {
    function approve(address, address, uint160, uint48) external { }
}

contract ReleaseMarketFeedMock is IChainlinkFeed {
    function decimals() external pure returns (uint8) {
        return 8;
    }

    function latestRoundData() external view returns (uint80, int256, uint256, uint256, uint80) {
        return (1, 100e8, block.timestamp, block.timestamp, 1);
    }
}

contract ReleaseInvalidFeedMock is IChainlinkFeed {
    function decimals() external pure returns (uint8) {
        return 18;
    }

    function latestRoundData() external view returns (uint80, int256, uint256, uint256, uint80) {
        return (1, 100e18, block.timestamp, block.timestamp, 1);
    }
}

contract ReleaseTimelockMock {
    uint256 private immutable delay;

    constructor(uint256 delay_) {
        delay = delay_;
    }

    function getMinDelay() external view returns (uint256) {
        return delay;
    }
}

contract DeployRwaUserMainnetV1Harness is DeployRwaUserMainnetV1 {
    function _expectedCodeHash(address account, ReleaseConfig memory)
        internal
        view
        override
        returns (bytes32)
    {
        return account.codehash;
    }
}

contract DeployRwaUserMainnetV1Test is Test {
    address private constant USDG = 0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168;
    address private constant AAPL = 0xaF3D76f1834A1d425780943C99Ea8A608f8a93f9;
    address private constant ROUTER = 0x8876789976dEcBfCbBbe364623C63652db8C0904;
    address private constant PERMIT2 = 0x000000000022D473030F116dDEE9F6B43aC78BA3;

    DeployRwaUserMainnetV1Harness private script;
    ReleaseTimelockMock private timelock;
    ReleaseMarketFeedMock private marketFeed;

    address private guardian = makeAddr("guardian");
    address private publisher1 = makeAddr("publisher-1");
    address private publisher2 = makeAddr("publisher-2");
    address private publisher3 = makeAddr("publisher-3");

    function setUp() public {
        vm.chainId(4663);
        _installCanonicalContracts();
        script = new DeployRwaUserMainnetV1Harness();
        timelock = new ReleaseTimelockMock(2 days);
        marketFeed = new ReleaseMarketFeedMock();
    }

    function testDeploysReproducibleDisabledRelease() public {
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        uint256 snapshot = vm.snapshotState();
        DeployRwaUserMainnetV1.Deployment memory first = script.deploy(config);

        assertEq(uint8(first.registry.globalMode()), uint8(IMainnetExecutionRegistry.Mode.Halted));
        assertFalse(first.registry.isFactoryApproved(address(first.factory)));
        assertEq(first.sequencerFeed.publisherCount(), 3);
        assertEq(first.sequencerFeed.quorum(), 2);
        assertEq(first.sequencerFeed.maxAge(), 60 seconds);
        assertEq(first.sequencerFeed.publisher1(), publisher1);
        assertEq(first.sequencerFeed.publisher2(), publisher2);
        assertEq(first.sequencerFeed.publisher3(), publisher3);

        IRwaUserVaultFactoryV1.Policy memory policy = first.factory.policy();
        assertEq(policy.settlementAsset, USDG);
        assertEq(policy.stockToken, AAPL);
        assertEq(policy.router, ROUTER);
        assertEq(policy.permit2, PERMIT2);
        assertEq(policy.poolKey.currency0, USDG);
        assertEq(policy.poolKey.currency1, AAPL);
        assertEq(policy.poolKey.fee, 3000);
        assertEq(policy.poolKey.tickSpacing, 60);
        assertEq(policy.poolKey.hooks, address(0));
        assertEq(policy.maxSpotNotional, 25e6);
        assertEq(policy.maxPairGross, 50e6);
        assertEq(policy.turnoverLimit, 50e6);
        assertEq(policy.turnoverWindow, 1 days);
        assertEq(policy.heartbeat, 60 seconds);
        assertEq(policy.policyVersion, 1);
        assertEq(first.factory.policyDigest(), keccak256(abi.encode(policy)));

        address firstFeed = address(first.sequencerFeed);
        address firstRegistry = address(first.registry);
        address firstFactory = address(first.factory);
        bytes32 firstDigest = first.factory.policyDigest();

        assertTrue(vm.revertToState(snapshot));
        DeployRwaUserMainnetV1.Deployment memory second = script.deploy(config);
        assertEq(address(second.sequencerFeed), firstFeed);
        assertEq(address(second.registry), firstRegistry);
        assertEq(address(second.factory), firstFactory);
        assertEq(second.factory.policyDigest(), firstDigest);
    }

    function testReturnsApprovalCalldataWithoutApprovingFactory() public {
        DeployRwaUserMainnetV1.Deployment memory deployment = script.deploy(_config());
        bytes memory approval = script.approvalCalldata(deployment);
        bytes memory expected = abi.encodeCall(
            MainnetExecutionRegistry.approveFactory,
            (address(deployment.factory), address(deployment.factory).codehash)
        );

        assertEq(approval, expected);
        assertEq(deployment.registry.configAdmin(), address(timelock));
        assertFalse(deployment.registry.isFactoryApproved(address(deployment.factory)));
        assertEq(
            uint8(deployment.registry.globalMode()), uint8(IMainnetExecutionRegistry.Mode.Halted)
        );
    }

    function testRejectsPublisherOverlap() public {
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        config.publisher3 = config.publisher1;
        vm.expectRevert(DeployRwaUserMainnetV1.InvalidConfiguration.selector);
        script.deploy(config);
    }

    function testRejectsControlRoleOverlap() public {
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        config.guardian = config.publisher1;
        vm.expectRevert(DeployRwaUserMainnetV1.InvalidConfiguration.selector);
        script.deploy(config);
    }

    function testRejectsCanonicalDependencyAsRole() public {
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        config.guardian = AAPL;
        vm.expectRevert(DeployRwaUserMainnetV1.InvalidConfiguration.selector);
        script.deploy(config);
    }

    function testRejectsMarketFeedCodeHashMismatch() public {
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        config.marketFeedCodeHash = keccak256("wrong-market-feed");
        vm.expectRevert(
            abi.encodeWithSelector(
                DeployRwaUserMainnetV1.CodeHashMismatch.selector,
                address(marketFeed),
                config.marketFeedCodeHash,
                address(marketFeed).codehash
            )
        );
        script.deploy(config);
    }

    function testRejectsUnexpectedMarketFeedDecimals() public {
        ReleaseInvalidFeedMock invalidFeed = new ReleaseInvalidFeedMock();
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        config.marketFeed = address(invalidFeed);
        config.marketFeedCodeHash = address(invalidFeed).codehash;
        vm.expectRevert(DeployRwaUserMainnetV1.InvalidConfiguration.selector);
        script.deploy(config);
    }

    function testRejectsTimelockDelayMismatch() public {
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        config.timelockMinDelay = 1 days;
        vm.expectRevert(DeployRwaUserMainnetV1.InvalidConfiguration.selector);
        script.deploy(config);
    }

    function testRejectsZeroDeploymentSalt() public {
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        config.deploymentSalt = bytes32(0);
        vm.expectRevert(DeployRwaUserMainnetV1.InvalidConfiguration.selector);
        script.deploy(config);
    }

    function testRejectsWrongChain() public {
        vm.chainId(1);
        vm.expectRevert(abi.encodeWithSelector(DeployRwaUserMainnetV1.UnexpectedChain.selector, 1));
        script.deploy(_config());
    }

    function _config() private view returns (DeployRwaUserMainnetV1.ReleaseConfig memory config) {
        config = DeployRwaUserMainnetV1.ReleaseConfig({
            timelock: address(timelock),
            timelockCodeHash: address(timelock).codehash,
            timelockMinDelay: 2 days,
            guardian: guardian,
            publisher1: publisher1,
            publisher2: publisher2,
            publisher3: publisher3,
            marketFeed: address(marketFeed),
            marketFeedCodeHash: address(marketFeed).codehash,
            deploymentSalt: keccak256("release-1")
        });
    }

    function _installCanonicalContracts() private {
        ReleaseUsdgMock usdg = new ReleaseUsdgMock();
        ReleaseAaplMock aapl = new ReleaseAaplMock();
        ReleaseRouterMock router = new ReleaseRouterMock();
        ReleasePermit2Mock permit2 = new ReleasePermit2Mock();
        vm.etch(USDG, address(usdg).code);
        vm.etch(AAPL, address(aapl).code);
        vm.etch(ROUTER, address(router).code);
        vm.etch(PERMIT2, address(permit2).code);
    }
}
