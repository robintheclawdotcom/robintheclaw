// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { ERC20 } from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import { IERC20Metadata } from "@openzeppelin/contracts/token/ERC20/extensions/IERC20Metadata.sol";
import { IPermit2AllowanceTransfer, IUniversalRouter } from "../src/interfaces/IUniswapV4.sol";
import { MandateRiskManagerV1 } from "../src/MandateRiskManagerV1.sol";
import { RwaDeploymentFactory } from "../src/RwaDeploymentFactory.sol";

contract FactoryGovernanceActor { }

contract FactorySettlementAsset is ERC20 {
    constructor() ERC20("Global Dollar", "USDG") { }

    function decimals() public pure override returns (uint8) {
        return 6;
    }
}

contract FactoryRouter { }

contract FactoryPermit2 { }

contract RwaDeploymentFactoryV1Test is Test {
    FactorySettlementAsset private asset;
    FactoryGovernanceActor private configAdmin;
    FactoryGovernanceActor private treasury;
    FactoryRouter private router;
    FactoryPermit2 private permit2;
    address private guardian;

    function setUp() public {
        asset = new FactorySettlementAsset();
        configAdmin = new FactoryGovernanceActor();
        treasury = new FactoryGovernanceActor();
        router = new FactoryRouter();
        permit2 = new FactoryPermit2();
        guardian = makeAddr("guardian");
    }

    function testDeploysHaltedUnfundedSystemWithFinalGovernance() public {
        RwaDeploymentFactory factory = new RwaDeploymentFactory(_config(address(0)));

        assertEq(factory.configAdmin(), address(configAdmin));
        assertEq(factory.treasury(), address(treasury));
        assertEq(factory.guardian(), guardian);
        assertEq(factory.initialAgent(), address(0));
        assertEq(factory.sequencerGate().configAdmin(), address(configAdmin));
        assertEq(address(factory.sequencerGate().source()), address(0));
        assertEq(factory.sequencerGate().expectedSourceCodeHash(), bytes32(0));

        (, int256 answer, uint256 startedAt, uint256 updatedAt,) =
            factory.sequencerGate().latestRoundData();
        assertEq(answer, 1);
        assertEq(startedAt, block.timestamp);
        assertEq(updatedAt, block.timestamp);

        assertEq(factory.riskManager().configAdmin(), address(configAdmin));
        assertEq(factory.riskManager().treasury(), address(treasury));
        assertEq(factory.riskManager().guardian(), guardian);
        assertEq(address(factory.riskManager().sequencerFeed()), address(factory.sequencerGate()));
        assertEq(factory.riskManager().executor(), address(factory.vault()));
        assertEq(uint8(factory.riskManager().mode()), uint8(MandateRiskManagerV1.Mode.Halted));
        assertEq(factory.riskManager().grossExposure(), 0);

        assertEq(factory.spotAdapter().configAdmin(), address(configAdmin));
        assertEq(factory.spotAdapter().vault(), address(factory.vault()));
        assertEq(factory.spotAdapter().routerCodeHash(), address(router).codehash);
        assertEq(factory.spotAdapter().permit2CodeHash(), address(permit2).codehash);

        assertEq(factory.vault().configAdmin(), address(configAdmin));
        assertEq(factory.vault().treasury(), address(treasury));
        assertEq(factory.vault().agent(), address(0));
        assertEq(asset.balanceOf(address(factory.vault())), 0);
        assertEq(address(factory.vault()).balance, 0);
    }

    function testAcceptsDistinctInitialAgent() public {
        address agent = makeAddr("agent");
        RwaDeploymentFactory factory = new RwaDeploymentFactory(_config(agent));

        assertEq(factory.initialAgent(), agent);
        assertEq(factory.vault().agent(), agent);
    }

    function testRejectsEOAGovernanceContracts() public {
        RwaDeploymentFactory.Config memory config = _config(address(0));
        config.configAdmin = makeAddr("eoa-admin");
        vm.expectRevert(RwaDeploymentFactory.InvalidRoles.selector);
        new RwaDeploymentFactory(config);

        config = _config(address(0));
        config.treasury = makeAddr("eoa-treasury");
        vm.expectRevert(RwaDeploymentFactory.InvalidRoles.selector);
        new RwaDeploymentFactory(config);
    }

    function testRejectsOverlappingRoles() public {
        RwaDeploymentFactory.Config memory config = _config(address(0));
        config.treasury = address(configAdmin);
        vm.expectRevert(RwaDeploymentFactory.InvalidRoles.selector);
        new RwaDeploymentFactory(config);

        config = _config(address(0));
        config.guardian = address(treasury);
        vm.expectRevert(RwaDeploymentFactory.InvalidRoles.selector);
        new RwaDeploymentFactory(config);

        config = _config(address(configAdmin));
        vm.expectRevert(RwaDeploymentFactory.InvalidRoles.selector);
        new RwaDeploymentFactory(config);
    }

    function _config(address agent) private view returns (RwaDeploymentFactory.Config memory) {
        return RwaDeploymentFactory.Config({
            settlementAsset: IERC20Metadata(address(asset)),
            router: IUniversalRouter(address(router)),
            permit2: IPermit2AllowanceTransfer(address(permit2)),
            configAdmin: address(configAdmin),
            treasury: address(treasury),
            guardian: guardian,
            agent: agent,
            routerCodeHash: address(router).codehash,
            permit2CodeHash: address(permit2).codehash,
            grossNotionalLimit: 25e6,
            turnoverLimit: 50e6,
            turnoverWindow: 1 days,
            maxDeadlineDelay: 60,
            sequencerGracePeriod: 1 hours,
            maxActiveMarkets: 1
        });
    }
}
