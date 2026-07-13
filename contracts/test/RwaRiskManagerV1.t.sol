// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { ERC20 } from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import { IChainlinkFeed } from "../src/interfaces/IChainlinkFeed.sol";
import { ISpotExecution } from "../src/interfaces/ISpotExecution.sol";
import {
    IPermit2AllowanceTransfer,
    IUniversalRouter,
    PoolKey
} from "../src/interfaces/IUniswapV4.sol";
import { MandateRiskManagerV1 } from "../src/MandateRiskManagerV1.sol";
import { UniswapV4SpotAdapter } from "../src/UniswapV4SpotAdapter.sol";

contract RiskRole { }

contract RiskToken is ERC20 {
    uint8 private immutable tokenDecimals;

    constructor(string memory symbol_, uint8 decimals_) ERC20(symbol_, symbol_) {
        tokenDecimals = decimals_;
    }

    function decimals() public view override returns (uint8) {
        return tokenDecimals;
    }
}

contract RiskStockToken is RiskToken {
    uint256 public uiMultiplier = 1e18;
    uint256 public newUIMultiplier = 1e18;
    uint256 public effectiveAt;
    bool public oraclePaused;

    constructor(string memory symbol_) RiskToken(symbol_, 18) { }

    function setMultiplier(uint256 current, uint256 pending, uint256 effective) external {
        uiMultiplier = current;
        newUIMultiplier = pending;
        effectiveAt = effective;
    }

    function setOraclePaused(bool paused) external {
        oraclePaused = paused;
    }
}

contract RiskFeed is IChainlinkFeed {
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

    function setRound(uint80 roundId_, int256 answer_, uint256 updatedAt_) external {
        roundId = roundId_;
        answer = answer_;
        updatedAt = updatedAt_;
        answeredInRound = roundId_;
    }

    function setSequencer(int256 answer_, uint256 startedAt_) external {
        answer = answer_;
        startedAt = startedAt_;
        updatedAt = block.timestamp;
    }

    function latestRoundData() external view returns (uint80, int256, uint256, uint256, uint80) {
        return (roundId, answer, startedAt, updatedAt, answeredInRound);
    }
}

    contract RiskExecutor {
        function authorize(MandateRiskManagerV1 risk, ISpotExecution.SpotIntent calldata intent)
            external
            returns (uint256, uint256, uint256)
        {
            return risk.authorize(intent);
        }

        function settle(MandateRiskManagerV1 risk, bytes32 id, uint256 actualIn, uint256 actualOut)
            external
        {
            risk.settle(id, actualIn, actualOut);
        }
    }

    contract AdapterPermit2 is IPermit2AllowanceTransfer {
        function approve(address, address, uint160, uint48) external { }
    }

    contract AdapterRouter is IUniversalRouter {
        function execute(bytes calldata, bytes[] calldata, uint256) external { }
    }

    contract AdapterVault {
        function execute(UniswapV4SpotAdapter adapter, ISpotExecution.SpotIntent calldata intent)
            external
            returns (uint256)
        {
            return adapter.executeSpot(intent);
        }
    }

    contract RwaRiskManagerV1Test is Test {
        RiskToken private settlement;
        RiskStockToken private stockA;
        RiskStockToken private stockB;
        RiskFeed private sequencer;
        RiskFeed private feedA;
        RiskFeed private feedB;
        RiskRole private configAdmin;
        RiskRole private treasury;
        RiskExecutor private executor;
        MandateRiskManagerV1 private risk;

        address private guardian = makeAddr("guardian");
        mapping(address stockToken => RiskFeed feed) private feeds;
        mapping(address stockToken => uint64 version) private versions;

        function setUp() public {
            vm.warp(2 days);
            settlement = new RiskToken("USDG", 6);
            stockA = new RiskStockToken("STOCK-A");
            stockB = new RiskStockToken("STOCK-B");
            sequencer = new RiskFeed(0, 0);
            sequencer.setSequencer(0, block.timestamp - 2 hours);
            feedA = new RiskFeed(8, 1e8);
            feedB = new RiskFeed(8, 1e8);
            configAdmin = new RiskRole();
            treasury = new RiskRole();
            executor = new RiskExecutor();

            risk = new MandateRiskManagerV1(
                settlement,
                sequencer,
                address(configAdmin),
                guardian,
                address(treasury),
                address(this),
                1_000e6,
                1_000e6,
                1 days,
                5 minutes,
                1 hours,
                3
            );
            risk.bindExecutor(address(executor));
            _setMarket(stockA, feedA, 1, 100, true, true);
            _setMarket(stockB, feedB, 1, 100, true, true);
            vm.prank(address(configAdmin));
            risk.setMode(MandateRiskManagerV1.Mode.Active);
        }

        function test_oracleFloorRejectsCompromisedBuyAndSell() public {
            ISpotExecution.SpotIntent memory badBuy =
                _intent(stockA, ISpotExecution.Side.BuySpot, 100e6, 98e18, bytes32("bad-buy"));
            vm.expectPartialRevert(MandateRiskManagerV1.SlippageLimitExceeded.selector);
            executor.authorize(risk, badBuy);

            _buy(stockA, 100e6, 99e18, 100e18, bytes32("seed"));
            ISpotExecution.SpotIntent memory badSell =
                _intent(stockA, ISpotExecution.Side.SellSpot, 100e18, 98e6, bytes32("bad-sell"));
            vm.expectPartialRevert(MandateRiskManagerV1.SlippageLimitExceeded.selector);
            executor.authorize(risk, badSell);
        }

        function test_intentBindsMultiplierAndMinimumOracleRound() public {
            ISpotExecution.SpotIntent memory wrongMultiplier =
                _intent(stockA, ISpotExecution.Side.BuySpot, 10e6, 99e17, bytes32("multiplier"));
            wrongMultiplier.expectedUIMultiplier = 2e18;
            vm.expectRevert(
                abi.encodeWithSelector(MandateRiskManagerV1.MultiplierMismatch.selector, 2e18, 1e18)
            );
            executor.authorize(risk, wrongMultiplier);

            ISpotExecution.SpotIntent memory futureRound =
                _intent(stockA, ISpotExecution.Side.BuySpot, 10e6, 99e17, bytes32("round"));
            futureRound.minOracleRoundId = 2;
            vm.expectRevert(
                abi.encodeWithSelector(MandateRiskManagerV1.OracleRoundTooOld.selector, 2, 1)
            );
            executor.authorize(risk, futureRound);
        }

        function test_reduceOnlyExitIgnoresExhaustedEntryTurnover() public {
            vm.prank(address(configAdmin));
            risk.setLimits(1_000e6, 100e6, 1 days, 5 minutes, 1 hours, 3);
            _buy(stockA, 100e6, 99e18, 100e18, bytes32("turnover-buy"));
            assertEq(risk.windowTurnover(), 100e6);

            vm.prank(address(configAdmin));
            risk.setMode(MandateRiskManagerV1.Mode.ReduceOnly);
            _sell(stockA, 100e18, 99e6, 99e6, bytes32("turnover-sell"));
            assertEq(risk.windowTurnover(), 100e6);
            assertEq(risk.inventory(address(stockA)), 0);
        }

        function test_unrelatedStaleFeedDoesNotBlockReduceOnlyExit() public {
            _buy(stockA, 100e6, 99e18, 100e18, bytes32("buy-a"));
            _buy(stockB, 100e6, 99e18, 100e18, bytes32("buy-b"));
            feedA.setRound(2, 1e8, block.timestamp - 2 hours);
            vm.prank(address(configAdmin));
            risk.setMode(MandateRiskManagerV1.Mode.ReduceOnly);

            _sell(stockB, 100e18, 99e6, 99e6, bytes32("sell-b"));
            assertEq(risk.inventory(address(stockB)), 0);
            assertEq(risk.activeMarketCount(), 1);
            assertEq(risk.activeMarketAt(0), address(stockA));
        }

        function test_currentMarketSafetyStillGatesExit() public {
            _buy(stockA, 100e6, 99e18, 100e18, bytes32("safe-buy"));
            feedA.setRound(2, 1e8, block.timestamp - 2 hours);
            vm.prank(address(configAdmin));
            risk.setMode(MandateRiskManagerV1.Mode.ReduceOnly);

            ISpotExecution.SpotIntent memory exit =
                _intent(stockA, ISpotExecution.Side.SellSpot, 100e18, 99e6, bytes32("stale-exit"));
            vm.expectPartialRevert(MandateRiskManagerV1.OracleStale.selector);
            executor.authorize(risk, exit);
        }

        function test_freshMarkBlocksEntryAfterAppreciation() public {
            vm.prank(address(configAdmin));
            risk.setLimits(150e6, 1_000e6, 1 days, 5 minutes, 1 hours, 3);
            _buy(stockA, 100e6, 99e18, 100e18, bytes32("mark-buy"));
            feedA.setRound(2, 2e8, block.timestamp);

            ISpotExecution.SpotIntent memory next =
                _intent(stockA, ISpotExecution.Side.BuySpot, 1e6, 5e17, bytes32("over-mark"));
            vm.expectPartialRevert(MandateRiskManagerV1.GrossLimitExceeded.selector);
            executor.authorize(risk, next);
        }

        function test_settlementRejectsOutputThatBreachesFreshMarkLimit() public {
            vm.prank(address(configAdmin));
            risk.setLimits(100e6, 1_000e6, 1 days, 5 minutes, 1 hours, 3);
            ISpotExecution.SpotIntent memory intent =
                _intent(stockA, ISpotExecution.Side.BuySpot, 10e6, 99e17, bytes32("settle-cap"));
            executor.authorize(risk, intent);
            vm.expectPartialRevert(MandateRiskManagerV1.GrossLimitExceeded.selector);
            executor.settle(risk, intent.id, 10e6, 101e18);
        }

        function test_activeMarketListRemainsBijectionAfterRemoval() public {
            _buy(stockA, 100e6, 99e18, 100e18, bytes32("list-a"));
            _buy(stockB, 100e6, 99e18, 100e18, bytes32("list-b"));
            _sell(stockA, 100e18, 99e6, 99e6, bytes32("remove-a"));

            assertEq(risk.activeMarketCount(), 1);
            assertEq(risk.activeMarketAt(0), address(stockB));
            assertEq(risk.grossExposure(), 100e6);
        }

        function test_feedPriceIsAlreadyMultiplierAdjusted() public {
            stockA.setMultiplier(2e18, 2e18, 0);
            feedA.setRound(2, 2e8, block.timestamp);
            _buy(stockA, 20e6, 99e17, 10e18, bytes32("adjusted"));
            assertEq(risk.grossExposure(), 20e6);
        }

        function test_marketConfigurationEnforcesSlippageCeiling() public {
            vm.prank(address(configAdmin));
            vm.expectRevert(MandateRiskManagerV1.InvalidConfiguration.selector);
            risk.setMarket(address(stockA), feedA, 1_000e6, 1_000e18, 1 hours, 2, 501, true, true);
        }

        function test_entryCanBeDisabledWithoutStrandingExit() public {
            _buy(stockA, 100e6, 99e18, 100e18, bytes32("disable-seed"));
            _setMarket(stockA, feedA, 2, 100, false, true);

            ISpotExecution.SpotIntent memory entry =
                _intent(stockA, ISpotExecution.Side.BuySpot, 1e6, 1e18, bytes32("disabled-entry"));
            vm.expectPartialRevert(MandateRiskManagerV1.MarketDisabled.selector);
            executor.authorize(risk, entry);

            vm.prank(address(configAdmin));
            risk.setMode(MandateRiskManagerV1.Mode.ReduceOnly);
            _sell(stockA, 100e18, 99e6, 99e6, bytes32("enabled-exit"));
        }

        function test_limitsCannotDropBelowActiveMarketCount() public {
            _buy(stockA, 100e6, 99e18, 100e18, bytes32("limit-a"));
            _buy(stockB, 100e6, 99e18, 100e18, bytes32("limit-b"));
            vm.prank(address(configAdmin));
            vm.expectRevert(MandateRiskManagerV1.InvalidConfiguration.selector);
            risk.setLimits(1_000e6, 1_000e6, 1 days, 5 minutes, 1 hours, 1);
        }

        function test_treasuryAndRotatedGuardianCanOnlyRestrict() public {
            vm.prank(address(treasury));
            risk.restrictMode(MandateRiskManagerV1.Mode.ReduceOnly);
            assertEq(uint8(risk.mode()), uint8(MandateRiskManagerV1.Mode.ReduceOnly));

            vm.prank(address(treasury));
            vm.expectRevert(MandateRiskManagerV1.InvalidModeTransition.selector);
            risk.restrictMode(MandateRiskManagerV1.Mode.Active);

            address nextGuardian = makeAddr("next-guardian");
            vm.prank(address(configAdmin));
            risk.setGuardian(nextGuardian);
            assertEq(risk.guardian(), nextGuardian);
            vm.prank(guardian);
            vm.expectRevert(MandateRiskManagerV1.NotRestrictor.selector);
            risk.restrictMode(MandateRiskManagerV1.Mode.Halted);
            vm.prank(nextGuardian);
            risk.restrictMode(MandateRiskManagerV1.Mode.Halted);
        }

        function test_adapterUsesIndependentEntryAndExitFlags() public {
            AdapterRouter router = new AdapterRouter();
            AdapterPermit2 permit2 = new AdapterPermit2();
            AdapterVault vault = new AdapterVault();
            UniswapV4SpotAdapter adapter = new UniswapV4SpotAdapter(
                settlement,
                router,
                permit2,
                address(configAdmin),
                address(this),
                address(router).codehash,
                address(permit2).codehash
            );
            adapter.bindVault(address(vault));
            vm.prank(address(configAdmin));
            adapter.setMarket(
                address(stockA),
                PoolKey({
                    currency0: address(settlement),
                    currency1: address(stockA),
                    fee: 500,
                    tickSpacing: 10,
                    hooks: address(0)
                }),
                1,
                false,
                true
            );

            ISpotExecution.SpotIntent memory entry =
                _intent(stockA, ISpotExecution.Side.BuySpot, 1e6, 1e18, bytes32("adapter-entry"));
            vm.expectPartialRevert(UniswapV4SpotAdapter.MarketDisabled.selector);
            vault.execute(adapter, entry);
            UniswapV4SpotAdapter.MarketRoute memory route = adapter.marketRoute(address(stockA));
            assertFalse(route.entryEnabled);
            assertTrue(route.exitEnabled);
        }

        function _setMarket(
            RiskStockToken stock,
            RiskFeed feed,
            uint64 version,
            uint16 maxSlippageBps,
            bool entryEnabled,
            bool exitEnabled
        ) private {
            feeds[address(stock)] = feed;
            versions[address(stock)] = version;
            vm.prank(address(configAdmin));
            risk.setMarket(
                address(stock),
                feed,
                1_000e6,
                1_000e18,
                1 hours,
                version,
                maxSlippageBps,
                entryEnabled,
                exitEnabled
            );
        }

        function _buy(
            RiskStockToken stock,
            uint128 amountIn,
            uint128 minOut,
            uint128 actualOut,
            bytes32 id
        ) private {
            ISpotExecution.SpotIntent memory intent =
                _intent(stock, ISpotExecution.Side.BuySpot, amountIn, minOut, id);
            executor.authorize(risk, intent);
            executor.settle(risk, id, amountIn, actualOut);
        }

        function _sell(
            RiskStockToken stock,
            uint128 amountIn,
            uint128 minOut,
            uint128 actualOut,
            bytes32 id
        ) private {
            ISpotExecution.SpotIntent memory intent = _intent(
                stock, ISpotExecution.Side.SellSpot, amountIn, minOut, id
            );
            executor.authorize(risk, intent);
            executor.settle(risk, id, amountIn, actualOut);
        }

        function _intent(
            RiskStockToken stock,
            ISpotExecution.Side side,
            uint128 amountIn,
            uint128 minOut,
            bytes32 id
        ) private view returns (ISpotExecution.SpotIntent memory) {
            RiskFeed feed = feeds[address(stock)];
            return ISpotExecution.SpotIntent({
                id: id,
                stockToken: address(stock),
                side: side,
                amountIn: amountIn,
                minAmountOut: minOut,
                expectedUIMultiplier: stock.uiMultiplier(),
                minOracleRoundId: feed.roundId(),
                deadline: uint64(block.timestamp + 2 minutes),
                configVersion: versions[address(stock)]
            });
        }
    }
