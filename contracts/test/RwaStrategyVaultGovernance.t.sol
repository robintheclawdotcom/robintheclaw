// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { ERC20 } from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { IChainlinkFeed } from "../src/interfaces/IChainlinkFeed.sol";
import { ISpotAdapter } from "../src/interfaces/ISpotAdapter.sol";
import { ISpotExecution } from "../src/interfaces/ISpotExecution.sol";
import { MandateRiskManagerV1 } from "../src/MandateRiskManagerV1.sol";
import { RwaStrategyVault } from "../src/RwaStrategyVault.sol";

contract GovernanceAuthority { }

contract GovernanceToken is ERC20 {
    uint8 private immutable tokenDecimals;
    uint256 private feeBps;

    constructor(string memory name_, string memory symbol_, uint8 decimals_) ERC20(name_, symbol_) {
        tokenDecimals = decimals_;
    }

    function decimals() public view override returns (uint8) {
        return tokenDecimals;
    }

    function mint(address account, uint256 amount) external {
        _mint(account, amount);
    }

    function setFeeBps(uint256 feeBps_) external {
        feeBps = feeBps_;
    }

    function _update(address from, address to, uint256 amount) internal override {
        if (feeBps == 0 || from == address(0) || to == address(0)) {
            super._update(from, to, amount);
            return;
        }
        uint256 fee = amount * feeBps / 10_000;
        super._update(from, address(0), fee);
        super._update(from, to, amount - fee);
    }
}

contract GovernanceFeed is IChainlinkFeed {
    uint8 public immutable override decimals;

    constructor(uint8 decimals_) {
        decimals = decimals_;
    }

    function latestRoundData() external view returns (uint80, int256, uint256, uint256, uint80) {
        return (1, 0, block.timestamp, block.timestamp, 1);
    }
}

    contract GovernanceSpotAdapter is ISpotAdapter {
        IERC20 public immutable settlementAsset;
        address public immutable configAdmin;

        constructor(IERC20 settlementAsset_, address configAdmin_) {
            settlementAsset = settlementAsset_;
            configAdmin = configAdmin_;
        }

        function executeSpot(ISpotExecution.SpotIntent calldata)
            external
            pure
            returns (uint256 amountOut)
        {
            return 0;
        }
    }

    contract RwaStrategyVaultGovernanceTest is Test {
        GovernanceToken private settlement;
        GovernanceToken private recoveryToken;
        MandateRiskManagerV1 private risk;
        GovernanceSpotAdapter private adapter;
        RwaStrategyVault private vault;

        address private configAdmin;
        address private treasury;
        address private agent = makeAddr("agent");
        address private outsider = makeAddr("outsider");

        function setUp() public {
            configAdmin = address(new GovernanceAuthority());
            treasury = address(new GovernanceAuthority());
            settlement = new GovernanceToken("Settlement", "SET", 6);
            recoveryToken = new GovernanceToken("Recovery", "REC", 18);
            GovernanceFeed sequencer = new GovernanceFeed(0);

            risk = new MandateRiskManagerV1(
                settlement,
                sequencer,
                configAdmin,
                makeAddr("guardian"),
                treasury,
                address(this),
                1_000e6,
                2_000e6,
                1 days,
                5 minutes,
                1 hours,
                2
            );
            adapter = new GovernanceSpotAdapter(settlement, configAdmin);
            vault = _deployVault(configAdmin, treasury, address(0));
            risk.bindExecutor(address(vault));
        }

        function testDeploymentSeparatesAuthorityAndAllowsNoAgent() public view {
            assertEq(vault.configAdmin(), configAdmin);
            assertEq(vault.treasury(), treasury);
            assertEq(vault.agent(), address(0));
            assertEq(address(vault.settlementAsset()), address(settlement));
        }

        function testConfigAdminInstallsAgentAndTreasuryRevokesImmediately() public {
            vm.prank(treasury);
            vm.expectRevert(RwaStrategyVault.NotConfigAdmin.selector);
            vault.setAgent(agent);

            vm.startPrank(configAdmin);
            vm.expectRevert(RwaStrategyVault.InvalidAddress.selector);
            vault.setAgent(address(0));
            vm.expectRevert(RwaStrategyVault.InvalidAddress.selector);
            vault.setAgent(treasury);
            vault.setAgent(agent);
            vm.stopPrank();
            assertEq(vault.agent(), agent);

            vm.prank(outsider);
            vm.expectRevert(RwaStrategyVault.NotTreasury.selector);
            vault.revokeAgent();

            vm.prank(treasury);
            vault.revokeAgent();
            assertEq(vault.agent(), address(0));

            vm.prank(agent);
            vm.expectRevert(RwaStrategyVault.NotAgent.selector);
            vault.anchorBatch(bytes32(uint256(1)), 1, 1);
            ISpotExecution.SpotIntent memory intent;
            vm.prank(agent);
            vm.expectRevert(RwaStrategyVault.NotAgent.selector);
            vault.executeSpot(intent);
        }

        function testOnlyTreasuryDepositsSettlementAsset() public {
            settlement.mint(treasury, 100e6);
            vm.prank(treasury);
            settlement.approve(address(vault), type(uint256).max);

            vm.prank(configAdmin);
            vm.expectRevert(RwaStrategyVault.NotTreasury.selector);
            vault.deposit(1e6);

            vm.prank(treasury);
            vm.expectRevert(RwaStrategyVault.InvalidAmount.selector);
            vault.deposit(0);

            vm.prank(treasury);
            vault.deposit(100e6);
            assertEq(settlement.balanceOf(address(vault)), 100e6);
            assertEq(recoveryToken.balanceOf(address(vault)), 0);
        }

        function testDepositRejectsFeeOnTransferSettlement() public {
            settlement.mint(treasury, 100e6);
            settlement.setFeeBps(100);
            vm.prank(treasury);
            settlement.approve(address(vault), type(uint256).max);

            vm.prank(treasury);
            vm.expectRevert(RwaStrategyVault.InvalidBalanceDelta.selector);
            vault.deposit(100e6);
            assertEq(settlement.balanceOf(address(vault)), 0);
            assertEq(settlement.balanceOf(treasury), 100e6);
        }

        function testRecoveryIsTerminalAndSupportsMultipleTokens() public {
            vm.prank(configAdmin);
            vault.setAgent(agent);
            settlement.mint(address(vault), 100e6);
            recoveryToken.mint(address(vault), 10e18);

            vm.prank(treasury);
            vm.expectRevert(RwaStrategyVault.RecoveryNotFinalized.selector);
            vault.recover(settlement, 1e6);

            vm.prank(configAdmin);
            risk.setMode(MandateRiskManagerV1.Mode.Active);
            vm.prank(treasury);
            vm.expectRevert(RwaStrategyVault.RecoveryRequiresHalt.selector);
            vault.finalizeRecovery();

            vm.prank(configAdmin);
            risk.setMode(MandateRiskManagerV1.Mode.Halted);
            vm.prank(treasury);
            vault.finalizeRecovery();
            assertTrue(vault.recoveryFinalized());
            assertEq(vault.agent(), address(0));

            vm.prank(treasury);
            vm.expectRevert(RwaStrategyVault.RecoveryFinalized.selector);
            vault.finalizeRecovery();
            vm.prank(configAdmin);
            vm.expectRevert(RwaStrategyVault.RecoveryFinalized.selector);
            vault.setAgent(agent);
            vm.prank(treasury);
            vm.expectRevert(RwaStrategyVault.RecoveryFinalized.selector);
            vault.deposit(1e6);

            vm.prank(outsider);
            vm.expectRevert(RwaStrategyVault.NotTreasury.selector);
            vault.recover(settlement, 1e6);
            vm.prank(treasury);
            vm.expectRevert(RwaStrategyVault.InvalidAmount.selector);
            vault.recover(settlement, 0);

            vm.startPrank(treasury);
            vault.recover(settlement, 100e6);
            vault.recover(recoveryToken, 4e18);
            vault.recover(recoveryToken, 6e18);
            vm.stopPrank();
            assertEq(settlement.balanceOf(treasury), 100e6);
            assertEq(recoveryToken.balanceOf(treasury), 10e18);

            vm.prank(configAdmin);
            risk.setMode(MandateRiskManagerV1.Mode.Active);
            recoveryToken.mint(address(vault), 1e18);
            vm.prank(treasury);
            vm.expectRevert(RwaStrategyVault.RecoveryRequiresHalt.selector);
            vault.recover(recoveryToken, 1e18);
        }

        function testConstructorRejectsOverlappingRoles() public {
            vm.expectRevert(RwaStrategyVault.InvalidAddress.selector);
            _deployVault(configAdmin, configAdmin, address(0));

            vm.expectRevert(RwaStrategyVault.InvalidAddress.selector);
            _deployVault(configAdmin, treasury, treasury);
        }

        function _deployVault(address configAdmin_, address treasury_, address agent_)
            private
            returns (RwaStrategyVault)
        {
            return new RwaStrategyVault(settlement, risk, adapter, configAdmin_, treasury_, agent_);
        }
    }
