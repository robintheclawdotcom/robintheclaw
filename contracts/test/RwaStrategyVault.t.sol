// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { ERC20 } from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { SafeERC20 } from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import { IChainlinkFeed } from "../src/interfaces/IChainlinkFeed.sol";
import { ISpotExecution } from "../src/interfaces/ISpotExecution.sol";
import {
    ExactInputSingleParams,
    IPermit2AllowanceTransfer,
    IUniversalRouter,
    PoolKey
} from "../src/interfaces/IUniswapV4.sol";
import { MandateRiskManagerV1 } from "../src/MandateRiskManagerV1.sol";
import { RwaDeploymentFactory } from "../src/RwaDeploymentFactory.sol";
import { RwaStrategyVault } from "../src/RwaStrategyVault.sol";
import { UniswapV4SpotAdapter } from "../src/UniswapV4SpotAdapter.sol";

contract MockToken is ERC20 {
    uint8 private immutable tokenDecimals;

    constructor(string memory name_, string memory symbol_, uint8 decimals_) ERC20(name_, symbol_) {
        tokenDecimals = decimals_;
    }

    function decimals() public view override returns (uint8) {
        return tokenDecimals;
    }

    function mint(address to, uint256 amount) external {
        _mint(to, amount);
    }
}

contract MockStockToken is MockToken {
    uint256 public uiMultiplier = 1e18;
    uint256 public newUIMultiplier = 1e18;
    uint256 public effectiveAt;
    bool public oraclePaused;

    constructor() MockToken("Stock Token", "STOCK", 18) { }

    function setMultiplier(uint256 current, uint256 pending, uint256 effective) external {
        uiMultiplier = current;
        newUIMultiplier = pending;
        effectiveAt = effective;
    }

    function setOraclePaused(bool paused) external {
        oraclePaused = paused;
    }
}

contract MockFeed is IChainlinkFeed {
    uint8 public immutable override decimals;
    uint80 public roundId = 1;
    int256 public answer;
    uint256 public startedAt;
    uint256 public updatedAt;
    uint80 public answeredInRound = 1;

    constructor(uint8 decimals_, int256 answer_) {
        decimals = decimals_;
        answer = answer_;
        startedAt = block.timestamp;
        updatedAt = block.timestamp;
    }

    function set(int256 answer_, uint256 startedAt_, uint256 updatedAt_) external {
        answer = answer_;
        startedAt = startedAt_;
        updatedAt = updatedAt_;
    }

    function latestRoundData() external view returns (uint80, int256, uint256, uint256, uint80) {
        return (roundId, answer, startedAt, updatedAt, answeredInRound);
    }
}

    contract MockAuthority { }

    contract MockPermit2 is IPermit2AllowanceTransfer {
        using SafeERC20 for IERC20;

        mapping(
            address owner => mapping(address token => mapping(address spender => uint160 amount))
        ) public allowances;

        function approve(address token, address spender, uint160 amount, uint48) external {
            allowances[msg.sender][token][spender] = amount;
        }

        function transferFrom(address from, address to, uint160 amount, address token) external {
            uint160 allowed = allowances[from][token][msg.sender];
            require(allowed >= amount, "permit2 allowance");
            allowances[from][token][msg.sender] = allowed - amount;
            IERC20(token).safeTransferFrom(from, to, amount);
        }
    }

    contract MockRouter is IUniversalRouter {
        using SafeERC20 for IERC20;

        MockPermit2 public immutable permit2;
        address public immutable settlementAsset;
        bool public malformedOutput;

        constructor(MockPermit2 permit2_, address settlementAsset_) {
            permit2 = permit2_;
            settlementAsset = settlementAsset_;
        }

        function setMalformedOutput(bool malformed) external {
            malformedOutput = malformed;
        }

        function execute(bytes calldata commands, bytes[] calldata inputs, uint256 deadline)
            external
        {
            require(commands.length == 1 && commands[0] == 0x10, "command");
            require(deadline >= block.timestamp, "deadline");
            (bytes memory actions, bytes[] memory params) = abi.decode(inputs[0], (bytes, bytes[]));
            require(actions.length == 3, "actions");
            ExactInputSingleParams memory swap = abi.decode(params[0], (ExactInputSingleParams));
            (address input, uint256 maxAmount) = abi.decode(params[1], (address, uint256));
            (address output, uint256 minAmount) = abi.decode(params[2], (address, uint256));
            require(maxAmount == swap.amountIn && minAmount == swap.amountOutMinimum, "params");

            permit2.transferFrom(msg.sender, address(this), swap.amountIn, input);
            uint256 outputAmount = input == settlementAsset
                ? uint256(swap.amountIn) * 1e12
                : uint256(swap.amountIn) / 1e12;
            if (malformedOutput) outputAmount = minAmount - 1;
            IERC20(output).safeTransfer(msg.sender, outputAmount);
        }
    }

    contract RwaStrategyVaultTest is Test {
        MockToken private usdg;
        MockStockToken private stock;
        MockFeed private sequencer;
        MockFeed private stockFeed;
        MockPermit2 private permit2;
        MockRouter private router;
        MandateRiskManagerV1 private risk;
        UniswapV4SpotAdapter private adapter;
        RwaStrategyVault private vault;
        MandateRiskManagerV1.Mode private deployedMode;
        uint256 private intentMultiplier = 1e18;

        address private admin;
        address private recovery;
        address private guardian = makeAddr("guardian");
        address private agent = makeAddr("agent");

        function setUp() public {
            vm.warp(2 days);
            admin = address(new MockAuthority());
            recovery = address(new MockAuthority());
            usdg = new MockToken("Global Dollar", "USDG", 6);
            stock = new MockStockToken();
            sequencer = new MockFeed(0, 0);
            sequencer.set(0, block.timestamp - 2 hours, block.timestamp);
            stockFeed = new MockFeed(8, 1e8);
            permit2 = new MockPermit2();
            router = new MockRouter(permit2, address(usdg));

            RwaDeploymentFactory factory = new RwaDeploymentFactory(
                RwaDeploymentFactory.Config({
                    settlementAsset: usdg,
                    router: router,
                    permit2: permit2,
                    configAdmin: admin,
                    treasury: recovery,
                    guardian: guardian,
                    agent: agent,
                    routerCodeHash: address(router).codehash,
                    permit2CodeHash: address(permit2).codehash,
                    grossNotionalLimit: 2_000e6,
                    turnoverLimit: 5_000e6,
                    turnoverWindow: 1 days,
                    maxDeadlineDelay: 5 minutes,
                    sequencerGracePeriod: 1 hours,
                    maxActiveMarkets: 2
                })
            );
            risk = factory.riskManager();
            adapter = factory.spotAdapter();
            vault = factory.vault();
            deployedMode = risk.mode();

            vm.startPrank(admin);
            factory.sequencerGate().bindSource(sequencer, address(sequencer).codehash);
            risk.setMarket(address(stock), stockFeed, 1_000e6, 2_000e18, 1 hours, 1, true);
            adapter.setMarket(
                address(stock),
                PoolKey({
                    currency0: address(usdg),
                    currency1: address(stock),
                    fee: 500,
                    tickSpacing: 10,
                    hooks: address(0)
                }),
                1,
                true
            );
            risk.setMode(MandateRiskManagerV1.Mode.Active);
            vm.stopPrank();

            usdg.mint(recovery, 10_000e6);
            stock.mint(address(router), 1_000_000e18);
            usdg.mint(address(router), 10_000e6);
            vm.startPrank(recovery);
            usdg.approve(address(vault), type(uint256).max);
            vault.deposit(5_000e6);
            vm.stopPrank();
        }

        function test_deploymentStartsFullyBoundAndHaltedBeforeConfiguration() public view {
            assertEq(uint8(deployedMode), uint8(MandateRiskManagerV1.Mode.Halted));
            assertEq(risk.executor(), address(vault));
            assertEq(adapter.vault(), address(vault));
            assertEq(vault.treasury(), recovery);
            assertEq(vault.attestationAnchor().publisher(), address(vault));
        }

        function test_adapterRejectsUnpinnedRouterCode() public {
            vm.expectRevert(UniswapV4SpotAdapter.InvalidConfiguration.selector);
            new UniswapV4SpotAdapter(
                usdg,
                router,
                permit2,
                admin,
                address(this),
                bytes32(uint256(1)),
                address(permit2).codehash
            );
        }

        function test_buyAndSellUseMeasuredDeltas() public {
            uint256 bought = _execute(ISpotExecution.Side.BuySpot, 500e6, 499e18, bytes32("buy"));
            assertEq(bought, 500e18);
            assertEq(risk.inventory(address(stock)), 500e18);
            assertEq(risk.inventoryCost(address(stock)), 500e6);
            assertEq(risk.grossExposure(), 500e6);
            assertEq(usdg.allowance(address(vault), address(adapter)), 0);
            assertEq(usdg.allowance(address(adapter), address(permit2)), 0);
            assertEq(permit2.allowances(address(adapter), address(usdg), address(router)), 0);

            uint256 proceeds = _execute(
                ISpotExecution.Side.SellSpot, 200e18, 199e6, bytes32("sell")
            );
            assertEq(proceeds, 200e6);
            assertEq(risk.inventory(address(stock)), 300e18);
            assertEq(risk.inventoryCost(address(stock)), 300e6);
            assertEq(risk.grossExposure(), 300e6);
        }

        function test_replayIsRejected() public {
            _execute(ISpotExecution.Side.BuySpot, 100e6, 99e18, bytes32("same"));
            vm.prank(agent);
            vm.expectRevert(
                abi.encodeWithSelector(
                    MandateRiskManagerV1.IntentAlreadyUsed.selector, bytes32("same")
                )
            );
            vault.executeSpot(_intent(ISpotExecution.Side.BuySpot, 100e6, 99e18, bytes32("same")));
        }

        function test_guardianCanHaltButCannotActivate() public {
            vm.prank(guardian);
            risk.restrictMode(MandateRiskManagerV1.Mode.Halted);
            vm.prank(agent);
            vm.expectRevert(MandateRiskManagerV1.Halted.selector);
            vault.executeSpot(_intent(ISpotExecution.Side.BuySpot, 1e6, 1e18, bytes32("halted")));

            vm.prank(guardian);
            vm.expectRevert(MandateRiskManagerV1.InvalidModeTransition.selector);
            risk.restrictMode(MandateRiskManagerV1.Mode.Active);
        }

        function test_deadlineAndSequencerAreEnforced() public {
            ISpotExecution.SpotIntent memory expired =
                _intent(ISpotExecution.Side.BuySpot, 1e6, 1e18, bytes32("expired"));
            expired.deadline = uint64(block.timestamp - 1);
            vm.prank(agent);
            vm.expectRevert(MandateRiskManagerV1.DeadlineExpired.selector);
            vault.executeSpot(expired);

            sequencer.set(1, block.timestamp - 2 hours, block.timestamp);
            vm.prank(agent);
            vm.expectRevert(MandateRiskManagerV1.SequencerDown.selector);
            vault.executeSpot(_intent(ISpotExecution.Side.BuySpot, 1e6, 1e18, bytes32("sequencer")));
        }

        function test_reduceOnlyAllowsExitButBlocksEntry() public {
            _execute(ISpotExecution.Side.BuySpot, 100e6, 100e18, bytes32("seed"));
            vm.prank(admin);
            risk.setMode(MandateRiskManagerV1.Mode.ReduceOnly);
            vm.prank(agent);
            vm.expectRevert(MandateRiskManagerV1.ReduceOnly.selector);
            vault.executeSpot(_intent(ISpotExecution.Side.BuySpot, 1e6, 1e18, bytes32("entry")));
            _execute(ISpotExecution.Side.SellSpot, 100e18, 100e6, bytes32("exit"));
            assertEq(risk.grossExposure(), 0);
        }

        function test_oracleAndCorporateActionVetoes() public {
            stock.setOraclePaused(true);
            vm.prank(agent);
            vm.expectRevert(
                abi.encodeWithSelector(MandateRiskManagerV1.OraclePaused.selector, address(stock))
            );
            vault.executeSpot(_intent(ISpotExecution.Side.BuySpot, 1e6, 1e18, bytes32("paused")));

            stock.setOraclePaused(false);
            stock.setMultiplier(1e18, 11e17, block.timestamp + 1 days);
            vm.prank(agent);
            vm.expectPartialRevert(MandateRiskManagerV1.MultiplierTransition.selector);
            vault.executeSpot(_intent(ISpotExecution.Side.BuySpot, 1e6, 1e18, bytes32("corp")));

            stock.setMultiplier(1e18, 1e18, 0);
            stockFeed.set(1e8, block.timestamp, block.timestamp - 2 hours);
            vm.prank(agent);
            vm.expectPartialRevert(MandateRiskManagerV1.OracleStale.selector);
            vault.executeSpot(_intent(ISpotExecution.Side.BuySpot, 1e6, 1e18, bytes32("stale")));
        }

        function test_sellNotionalUsesAlreadyAdjustedPerTokenOraclePrice() public {
            _execute(ISpotExecution.Side.BuySpot, 100e6, 100e18, bytes32("seed-price"));
            stock.setMultiplier(2e18, 2e18, 0);
            intentMultiplier = 2e18;
            stockFeed.set(1e8, block.timestamp, block.timestamp);

            _execute(ISpotExecution.Side.SellSpot, 5e18, 5e6, bytes32("priced-sell"));
            assertEq(risk.windowTurnover(), 105e6);
        }

        function test_recoveryRequiresHaltAndPaysSafeRecipient() public {
            uint256 treasuryBalanceBefore = usdg.balanceOf(recovery);
            vm.prank(recovery);
            vm.expectRevert(RwaStrategyVault.RecoveryRequiresHalt.selector);
            vault.recover(usdg, 10e6);

            vm.prank(admin);
            risk.setMode(MandateRiskManagerV1.Mode.Halted);
            vm.startPrank(recovery);
            vault.finalizeRecovery();
            vault.recover(usdg, 10e6);
            vm.stopPrank();
            assertEq(usdg.balanceOf(recovery), treasuryBalanceBefore + 10e6);
            assertTrue(vault.recoveryFinalized());

            vm.prank(admin);
            risk.setMode(MandateRiskManagerV1.Mode.Active);
            vm.prank(agent);
            vm.expectRevert(RwaStrategyVault.NotAgent.selector);
            vault.executeSpot(_intent(ISpotExecution.Side.BuySpot, 1e6, 1e18, bytes32("final")));

            vm.prank(recovery);
            vm.expectRevert(RwaStrategyVault.RecoveryFinalized.selector);
            vault.deposit(1e6);
        }

        function test_limitsAndRouteVersionAreEnforced() public {
            vm.prank(agent);
            vm.expectPartialRevert(MandateRiskManagerV1.OrderLimitExceeded.selector);
            vault.executeSpot(_intent(ISpotExecution.Side.BuySpot, 1_001e6, 1e18, bytes32("large")));

            ISpotExecution.SpotIntent memory stale =
                _intent(ISpotExecution.Side.BuySpot, 1e6, 1e18, bytes32("version"));
            stale.configVersion = 2;
            vm.prank(agent);
            vm.expectPartialRevert(MandateRiskManagerV1.StaleConfiguration.selector);
            vault.executeSpot(stale);
        }

        function test_routerUnderfillRevertsAtomically() public {
            router.setMalformedOutput(true);
            vm.prank(agent);
            vm.expectRevert(UniswapV4SpotAdapter.InvalidBalanceDelta.selector);
            vault.executeSpot(
                _intent(ISpotExecution.Side.BuySpot, 100e6, 100e18, bytes32("underfill"))
            );
            assertFalse(risk.usedIntent(bytes32("underfill")));
            assertEq(risk.grossExposure(), 0);
        }

        function test_nonAgentCannotExecuteOrAnchor() public {
            vm.expectRevert(RwaStrategyVault.NotAgent.selector);
            vault.executeSpot(_intent(ISpotExecution.Side.BuySpot, 1e6, 1e18, bytes32("unauth")));
            vm.expectRevert(RwaStrategyVault.NotAgent.selector);
            vault.anchorBatch(bytes32(uint256(1)), 1, 1);
        }

        function testFuzz_buyMaintainsCostInvariant(uint96 amount) public {
            amount = uint96(bound(amount, 1e6, 1_000e6));
            uint128 minOut = uint128(uint256(amount) * 1e12);
            _execute(ISpotExecution.Side.BuySpot, amount, minOut, keccak256(abi.encode(amount)));
            assertEq(risk.inventoryCost(address(stock)), amount);
            assertEq(risk.grossExposure(), amount);
        }

        function _execute(ISpotExecution.Side side, uint128 amountIn, uint128 minOut, bytes32 id)
            private
            returns (uint256)
        {
            vm.prank(agent);
            return vault.executeSpot(_intent(side, amountIn, minOut, id));
        }

        function _intent(ISpotExecution.Side side, uint128 amountIn, uint128 minOut, bytes32 id)
            private
            view
            returns (ISpotExecution.SpotIntent memory)
        {
            return ISpotExecution.SpotIntent({
                id: id,
                stockToken: address(stock),
                side: side,
                amountIn: amountIn,
                minAmountOut: minOut,
                deadline: uint64(block.timestamp + 2 minutes),
                configVersion: 1,
                expectedUIMultiplier: intentMultiplier,
                minOracleRoundId: 1
            });
        }
    }
