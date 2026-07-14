// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { ERC20 } from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import { DeployRwaUserMainnetV1 } from "../script/DeployRwaUserMainnetV1.s.sol";
import { MainnetExecutionRegistry } from "../src/MainnetExecutionRegistry.sol";
import { IMainnetExecutionRegistry } from "../src/interfaces/IMainnetExecutionRegistry.sol";
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

contract ReleaseGovernanceSafeMock { }

contract ReleaseTimelockMock {
    bytes32 public constant PROPOSER_ROLE = keccak256("PROPOSER_ROLE");
    bytes32 public constant CANCELLER_ROLE = keccak256("CANCELLER_ROLE");
    bytes32 public constant EXECUTOR_ROLE = keccak256("EXECUTOR_ROLE");
    bytes32 public constant DEFAULT_ADMIN_ROLE = bytes32(0);

    uint256 private immutable delay;
    address private immutable safe;
    bool private openExecutor;

    constructor(uint256 delay_, address safe_) {
        delay = delay_;
        safe = safe_;
    }

    function getMinDelay() external view returns (uint256) {
        return delay;
    }

    function hasRole(bytes32 role, address account) external view returns (bool) {
        if (role == DEFAULT_ADMIN_ROLE) return account == address(this);
        if (account == safe) {
            return role == PROPOSER_ROLE || role == CANCELLER_ROLE || role == EXECUTOR_ROLE;
        }
        return openExecutor && role == EXECUTOR_ROLE && account == address(0);
    }

    function setOpenExecutor(bool open) external {
        openExecutor = open;
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
    ReleaseGovernanceSafeMock private governanceSafe;
    address private guardian = makeAddr("guardian");
    address private publisher1 = makeAddr("publisher-1");
    address private publisher2 = makeAddr("publisher-2");
    address private publisher3 = makeAddr("publisher-3");
    address private aaplPublisher1 = makeAddr("aapl-publisher-1");
    address private aaplPublisher2 = makeAddr("aapl-publisher-2");
    address private aaplPublisher3 = makeAddr("aapl-publisher-3");

    function setUp() public {
        vm.chainId(4663);
        _installCanonicalContracts();
        script = new DeployRwaUserMainnetV1Harness();
        governanceSafe = new ReleaseGovernanceSafeMock();
        timelock = new ReleaseTimelockMock(2 days, address(governanceSafe));
    }

    function testDeploysReproducibleRelease() public {
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
        assertEq(first.marketFeed.publisherCount(), 3);
        assertEq(first.marketFeed.quorum(), 2);
        assertEq(first.marketFeed.maxReportAge(), 60 seconds);
        assertEq(first.marketFeed.maxSourceAge(), 25 hours);
        assertEq(first.marketFeed.publisher1(), aaplPublisher1);
        assertEq(first.marketFeed.publisher2(), aaplPublisher2);
        assertEq(first.marketFeed.publisher3(), aaplPublisher3);

        IRwaUserVaultFactoryV1.Policy memory policy = first.factory.policy();
        assertEq(policy.settlementAsset, USDG);
        assertEq(policy.stockToken, AAPL);
        assertEq(policy.marketFeed, address(first.marketFeed));
        assertEq(policy.router, ROUTER);
        assertEq(policy.permit2, PERMIT2);
        assertEq(policy.poolKey.currency0, USDG);
        assertEq(policy.poolKey.currency1, AAPL);
        assertEq(policy.poolKey.fee, 10_000);
        assertEq(policy.poolKey.tickSpacing, 200);
        assertEq(policy.poolKey.hooks, address(0));
        assertEq(
            keccak256(abi.encode(policy.poolKey)),
            0xda4116b5894ee7479e64eae9276e1a2944ef0e5ce863a299d296a15618deee01
        );
        assertEq(policy.maxSpotNotional, 25e6);
        assertEq(policy.maxPairGross, 50e6);
        assertEq(policy.turnoverLimit, 50e6);
        assertEq(policy.turnoverWindow, 1 days);
        assertEq(policy.heartbeat, 25 hours);
        assertEq(policy.maxSlippageBps, 200);
        assertEq(policy.policyVersion, 1);
        assertEq(first.factory.policyDigest(), keccak256(abi.encode(policy)));

        address firstFeed = address(first.sequencerFeed);
        address firstMarketFeed = address(first.marketFeed);
        address firstRegistry = address(first.registry);
        address firstFactory = address(first.factory);
        bytes32 firstDigest = first.factory.policyDigest();

        assertTrue(vm.revertToState(snapshot));
        DeployRwaUserMainnetV1.Deployment memory second = script.deploy(config);
        assertEq(address(second.sequencerFeed), firstFeed);
        assertEq(address(second.marketFeed), firstMarketFeed);
        assertEq(address(second.registry), firstRegistry);
        assertEq(address(second.factory), firstFactory);
        assertEq(second.factory.policyDigest(), firstDigest);
    }

    function testReturnsOneActivationBatch() public {
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        DeployRwaUserMainnetV1.Deployment memory deployment = script.deploy(_config());
        DeployRwaUserMainnetV1.ActivationBatch memory batch =
            script.activationBatch(config, deployment);
        bytes memory expectedApproval = abi.encodeCall(
            MainnetExecutionRegistry.approveFactory,
            (address(deployment.factory), address(deployment.factory).codehash)
        );
        bytes memory expectedActive = abi.encodeCall(
            MainnetExecutionRegistry.setGlobalMode, (IMainnetExecutionRegistry.Mode.Active)
        );

        assertEq(batch.targets.length, 2);
        assertEq(batch.values.length, 2);
        assertEq(batch.payloads.length, 2);
        assertEq(batch.targets[0], address(deployment.registry));
        assertEq(batch.targets[1], address(deployment.registry));
        assertEq(batch.values[0], 0);
        assertEq(batch.values[1], 0);
        assertEq(batch.payloads[0], expectedApproval);
        assertEq(batch.payloads[1], expectedActive);
        assertEq(batch.delay, 2 days);
        assertTrue(script.scheduleActivationCalldata(config, deployment).length > 4);
        assertTrue(script.executeActivationCalldata(config, deployment).length > 4);
        assertEq(
            script.activationOperationId(config, deployment),
            keccak256(
                abi.encode(
                    batch.targets, batch.values, batch.payloads, batch.predecessor, batch.salt
                )
            )
        );
        assertEq(deployment.registry.configAdmin(), address(timelock));
        assertFalse(deployment.registry.isFactoryApproved(address(deployment.factory)));
        assertEq(
            uint8(deployment.registry.globalMode()), uint8(IMainnetExecutionRegistry.Mode.Halted)
        );
    }

    function testRejectsPublisherOverlap() public {
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        config.sequencerPublisher3 = config.sequencerPublisher1;
        vm.expectRevert(DeployRwaUserMainnetV1.InvalidConfiguration.selector);
        script.deploy(config);
    }

    function testRejectsControlRoleOverlap() public {
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        config.guardian = config.sequencerPublisher1;
        vm.expectRevert(DeployRwaUserMainnetV1.InvalidConfiguration.selector);
        script.deploy(config);
    }

    function testRejectsCanonicalDependencyAsRole() public {
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        config.guardian = AAPL;
        vm.expectRevert(DeployRwaUserMainnetV1.InvalidConfiguration.selector);
        script.deploy(config);
    }

    function testRejectsRelayAndSequencerRoleOverlap() public {
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        config.aaplPublisher1 = config.sequencerPublisher1;
        vm.expectRevert(DeployRwaUserMainnetV1.InvalidConfiguration.selector);
        script.deploy(config);
    }

    function testRejectsTimelockDelayMismatch() public {
        DeployRwaUserMainnetV1.ReleaseConfig memory config = _config();
        config.timelockMinDelay = 1 days;
        vm.expectRevert(DeployRwaUserMainnetV1.InvalidConfiguration.selector);
        script.deploy(config);
    }

    function testRejectsOpenTimelockExecutor() public {
        timelock.setOpenExecutor(true);
        vm.expectRevert(DeployRwaUserMainnetV1.InvalidConfiguration.selector);
        script.deploy(_config());
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
            governanceSafe: address(governanceSafe),
            guardian: guardian,
            sequencerPublisher1: publisher1,
            sequencerPublisher2: publisher2,
            sequencerPublisher3: publisher3,
            aaplPublisher1: aaplPublisher1,
            aaplPublisher2: aaplPublisher2,
            aaplPublisher3: aaplPublisher3,
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
