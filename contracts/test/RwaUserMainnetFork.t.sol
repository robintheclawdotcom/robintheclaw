// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { Math } from "@openzeppelin/contracts/utils/math/Math.sol";
import { SafeCast } from "@openzeppelin/contracts/utils/math/SafeCast.sol";
import { MainnetExecutionRegistry } from "../src/MainnetExecutionRegistry.sol";
import { MandateRiskManagerV1 } from "../src/MandateRiskManagerV1.sol";
import { QuorumAaplReferenceFeed } from "../src/QuorumAaplReferenceFeed.sol";
import { QuorumSequencerFeed } from "../src/QuorumSequencerFeed.sol";
import { RwaUserStrategyVaultV1 } from "../src/RwaUserStrategyVaultV1.sol";
import { RwaUserVaultFactoryV1 } from "../src/RwaUserVaultFactoryV1.sol";
import { UniswapV4SpotAdapter } from "../src/UniswapV4SpotAdapter.sol";
import { IMainnetExecutionRegistry } from "../src/interfaces/IMainnetExecutionRegistry.sol";
import { ISpotExecution } from "../src/interfaces/ISpotExecution.sol";
import { PoolKey } from "../src/interfaces/IUniswapV4.sol";
import { IRwaUserVaultFactoryV1 } from "../src/interfaces/IRwaUserVaultFactoryV1.sol";

interface IV4Quoter {
    struct QuoteExactInputSingleParams {
        PoolKey poolKey;
        bool zeroForOne;
        uint128 exactAmount;
        bytes hookData;
    }

    function quoteExactInputSingle(QuoteExactInputSingleParams calldata params)
        external
        returns (uint256 amountOut, uint256 gasEstimate);
}

interface IPermit2AllowanceView {
    function allowance(address owner, address token, address spender)
        external
        view
        returns (uint160 amount, uint48 expiration, uint48 nonce);
}

contract RwaUserMainnetForkTest is Test {
    uint256 private constant CHAIN_ID = 4663;
    address private constant USDG = 0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168;
    address private constant AAPL = 0xaF3D76f1834A1d425780943C99Ea8A608f8a93f9;
    address private constant ROUTER = 0x8876789976dEcBfCbBbe364623C63652db8C0904;
    address private constant PERMIT2 = 0x000000000022D473030F116dDEE9F6B43aC78BA3;
    address private constant QUOTER = 0x8Dc178eFB8111BB0973Dd9d722ebeFF267c98F94;

    bytes32 private constant USDG_CODE_HASH =
        0x864cc9ad53b338b82da1f7cab85ab0b3d5c8861acb422b6fec63cf36234f36a6;
    bytes32 private constant AAPL_CODE_HASH =
        0x6c1fdd40002dcb440c7fff6a84171404d279ccb057803b65826f7546acd65630;
    bytes32 private constant ROUTER_CODE_HASH =
        0x2ce6aaaf9f4151f5e1cbf774668772f17f532ae11b15e9284fd0a072a8b0fbde;
    bytes32 private constant PERMIT2_CODE_HASH =
        0x5208783f52488f7d3493e5e38311ab707c1d75457fe472a19b0b4d57d66a7fca;
    bytes32 private constant QUOTER_CODE_HASH =
        0xd707b1da8cb165e5ea35a3b4450d971eb562ec171e23492aa117036b78a868f6;

    PoolKey private poolKey;

    function setUp() public {
        string memory rpc = vm.envOr("RH_MAINNET_RPC", string(""));
        if (bytes(rpc).length == 0) {
            vm.skip(true, "RH_MAINNET_RPC is not set");
            return;
        }

        vm.createSelectFork(rpc);
        assertEq(block.chainid, CHAIN_ID);
        assertEq(USDG.codehash, USDG_CODE_HASH);
        assertEq(AAPL.codehash, AAPL_CODE_HASH);
        assertEq(ROUTER.codehash, ROUTER_CODE_HASH);
        assertEq(PERMIT2.codehash, PERMIT2_CODE_HASH);
        assertEq(QUOTER.codehash, QUOTER_CODE_HASH);

        poolKey = PoolKey({
            currency0: USDG, currency1: AAPL, fee: 10_000, tickSpacing: 200, hooks: address(0)
        });
    }

    function testPinnedMainnetRoundTripAndWithdrawal() public {
        uint128 entryAmount = 25e6;
        uint256 quotedStock = _quote(true, entryAmount);
        uint256 referencePrice = Math.mulDiv(entryAmount, 1e20, quotedStock);
        referencePrice = Math.mulDiv(referencePrice, 9_950, 10_000);

        address guardian = makeAddr("guardian");
        address publisher1 = makeAddr("sequencer-publisher-1");
        address publisher2 = makeAddr("sequencer-publisher-2");
        address publisher3 = makeAddr("sequencer-publisher-3");
        address pricePublisher1 = makeAddr("price-publisher-1");
        address pricePublisher2 = makeAddr("price-publisher-2");
        address pricePublisher3 = makeAddr("price-publisher-3");
        address owner = makeAddr("owner");
        address agent = makeAddr("agent");

        MainnetExecutionRegistry registry = new MainnetExecutionRegistry(address(this), guardian);
        QuorumSequencerFeed sequencer = new QuorumSequencerFeed(publisher1, publisher2, publisher3);
        QuorumAaplReferenceFeed market =
            new QuorumAaplReferenceFeed(pricePublisher1, pricePublisher2, pricePublisher3);

        vm.prank(publisher1);
        sequencer.report(1, true, uint64(block.timestamp - 1 hours));
        vm.prank(publisher2);
        sequencer.report(1, true, uint64(block.timestamp - 1 hours));
        vm.prank(pricePublisher1);
        market.report(
            1,
            1,
            SafeCast.toInt192(SafeCast.toInt256(referencePrice)),
            uint64(block.timestamp - 1),
            1
        );
        vm.prank(pricePublisher2);
        market.report(
            1,
            1,
            SafeCast.toInt192(SafeCast.toInt256(referencePrice)),
            uint64(block.timestamp - 1),
            1
        );

        RwaUserVaultFactoryV1 factory =
            new RwaUserVaultFactoryV1(registry, _policy(market, sequencer));
        registry.approveFactory(address(factory), address(factory).codehash);
        registry.setGlobalMode(IMainnetExecutionRegistry.Mode.Active);

        IRwaUserVaultFactoryV1.Graph memory graph = factory.deploy(owner);
        registry.setVaultMode(graph.vault, IMainnetExecutionRegistry.Mode.Active);
        registry.setVaultAgent(graph.vault, agent);

        RwaUserStrategyVaultV1 vault = RwaUserStrategyVaultV1(graph.vault);
        MandateRiskManagerV1 risk = MandateRiskManagerV1(graph.riskManager);
        UniswapV4SpotAdapter adapter = UniswapV4SpotAdapter(graph.spotAdapter);

        deal(USDG, owner, entryAmount);
        vm.startPrank(owner);
        IERC20(USDG).approve(address(vault), entryAmount);
        vault.deposit(entryAmount);
        vault.enableAgent();
        vm.stopPrank();

        vm.prank(agent);
        vault.executeSpot(
            ISpotExecution.SpotIntent({
                id: keccak256("mainnet-fork-entry"),
                stockToken: AAPL,
                side: ISpotExecution.Side.BuySpot,
                amountIn: entryAmount,
                minAmountOut: uint128(Math.mulDiv(quotedStock, 9_900, 10_000)),
                expectedUIMultiplier: 1e18,
                minOracleRoundId: 1,
                deadline: uint64(block.timestamp + 1 minutes),
                configVersion: 1
            })
        );

        uint256 stockAmount = risk.inventory(AAPL);
        assertGt(stockAmount, 0);
        _assertAllowancesCleared(USDG, address(vault), address(adapter));
        assertEq(IERC20(USDG).balanceOf(address(adapter)), 0);
        assertEq(IERC20(AAPL).balanceOf(address(adapter)), 0);

        uint256 exitMinimum = _oracleExitMinimum(stockAmount, referencePrice);
        assertGt(_quote(false, uint128(stockAmount)), exitMinimum);
        vm.prank(agent);
        vault.executeSpot(
            ISpotExecution.SpotIntent({
                id: keccak256("mainnet-fork-exit"),
                stockToken: AAPL,
                side: ISpotExecution.Side.SellSpot,
                amountIn: uint128(stockAmount),
                minAmountOut: uint128(exitMinimum),
                expectedUIMultiplier: 1e18,
                minOracleRoundId: 1,
                deadline: uint64(block.timestamp + 1 minutes),
                configVersion: 1
            })
        );

        assertEq(risk.inventory(AAPL), 0);
        assertEq(risk.activeMarketCount(), 0);
        assertEq(risk.windowTurnover(), 50e6);
        _assertAllowancesCleared(AAPL, address(vault), address(adapter));
        assertEq(IERC20(USDG).balanceOf(address(adapter)), 0);
        assertEq(IERC20(AAPL).balanceOf(address(adapter)), 0);

        uint256 withdrawal = IERC20(USDG).balanceOf(address(vault));
        vm.startPrank(owner);
        vault.emergencyHalt();
        vault.withdrawSettlement(withdrawal);
        vm.stopPrank();

        assertEq(uint8(risk.mode()), uint8(MandateRiskManagerV1.Mode.Halted));
        assertEq(vault.agent(), address(0));
        assertFalse(vault.agentEnabled());
        assertEq(IERC20(USDG).balanceOf(owner), withdrawal);
        assertEq(IERC20(USDG).balanceOf(address(vault)), 0);
        assertEq(IERC20(AAPL).balanceOf(address(vault)), 0);
    }

    function _policy(QuorumAaplReferenceFeed market, QuorumSequencerFeed sequencer)
        private
        view
        returns (IRwaUserVaultFactoryV1.Policy memory)
    {
        return IRwaUserVaultFactoryV1.Policy({
            settlementAsset: USDG,
            stockToken: AAPL,
            marketFeed: address(market),
            sequencerFeed: address(sequencer),
            router: ROUTER,
            permit2: PERMIT2,
            poolKey: poolKey,
            settlementAssetCodeHash: USDG_CODE_HASH,
            stockTokenCodeHash: AAPL_CODE_HASH,
            marketFeedCodeHash: address(market).codehash,
            sequencerFeedCodeHash: address(sequencer).codehash,
            routerCodeHash: ROUTER_CODE_HASH,
            permit2CodeHash: PERMIT2_CODE_HASH,
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

    function _quote(bool zeroForOne, uint128 amountIn) private returns (uint256 amountOut) {
        (amountOut,) = IV4Quoter(QUOTER)
            .quoteExactInputSingle(
                IV4Quoter.QuoteExactInputSingleParams({
                    poolKey: poolKey,
                    zeroForOne: zeroForOne,
                    exactAmount: amountIn,
                    hookData: bytes("")
                })
            );
        assertGt(amountOut, 0);
    }

    function _oracleExitMinimum(uint256 stockAmount, uint256 referencePrice)
        private
        pure
        returns (uint256)
    {
        uint256 valueAtFeedDecimals =
            Math.mulDiv(stockAmount, referencePrice, 1e18, Math.Rounding.Ceil);
        uint256 notional = Math.mulDiv(valueAtFeedDecimals, 1e6, 1e8, Math.Rounding.Ceil);
        return Math.mulDiv(notional, 9_800, 10_000, Math.Rounding.Ceil);
    }

    function _assertAllowancesCleared(address token, address vault, address adapter) private view {
        assertEq(IERC20(token).allowance(vault, adapter), 0);
        assertEq(IERC20(token).allowance(adapter, PERMIT2), 0);
        (uint160 amount,,) = IPermit2AllowanceView(PERMIT2).allowance(adapter, token, ROUTER);
        assertEq(amount, 0);
    }
}
