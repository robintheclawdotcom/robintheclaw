// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IERC20Metadata } from "@openzeppelin/contracts/token/ERC20/extensions/IERC20Metadata.sol";
import { Math } from "@openzeppelin/contracts/utils/math/Math.sol";
import { SafeCast } from "@openzeppelin/contracts/utils/math/SafeCast.sol";
import { IChainlinkFeed } from "./interfaces/IChainlinkFeed.sol";
import { IRobinhoodStockToken } from "./interfaces/IRobinhoodStockToken.sol";
import { ISpotExecution } from "./interfaces/ISpotExecution.sol";

contract MandateRiskManagerV1 {
    enum Mode {
        Active,
        ReduceOnly,
        Halted
    }

    struct MarketConfig {
        IChainlinkFeed feed;
        uint128 maxOrderNotional;
        uint128 maxInventory;
        uint64 heartbeat;
        uint64 version;
        uint8 tokenDecimals;
        uint8 feedDecimals;
        bool enabled;
    }

    struct PendingExecution {
        bytes32 id;
        address stockToken;
        ISpotExecution.Side side;
        uint128 amountIn;
        uint128 minAmountOut;
    }

    IERC20Metadata public immutable settlementAsset;
    IChainlinkFeed public immutable sequencerFeed;
    address public immutable admin;
    address public immutable guardian;
    uint8 public immutable settlementDecimals;

    address public executor;
    address private bootstrapper;
    Mode public mode;
    uint64 public sequencerGracePeriod;
    uint64 public maxDeadlineDelay;
    uint64 public turnoverWindow;
    uint8 public maxActiveMarkets;
    uint256 public grossNotionalLimit;
    uint256 public turnoverLimit;
    uint256 public turnoverWindowStart;
    uint256 public windowTurnover;
    uint256 public grossExposure;
    uint256 public activeMarketCount;

    PendingExecution private pending;
    mapping(address => MarketConfig) public markets;
    mapping(address => uint256) public inventory;
    mapping(address => uint256) public inventoryCost;
    mapping(bytes32 => bool) public usedIntent;

    event ExecutorBound(address indexed executor);
    event ModeSet(Mode mode, address indexed caller);
    event LimitsSet(
        uint256 grossNotionalLimit,
        uint256 turnoverLimit,
        uint64 turnoverWindow,
        uint64 maxDeadlineDelay,
        uint64 sequencerGracePeriod,
        uint8 maxActiveMarkets
    );
    event MarketSet(
        address indexed stockToken,
        address indexed feed,
        uint64 indexed version,
        uint128 maxOrderNotional,
        uint128 maxInventory,
        uint64 heartbeat,
        bool enabled
    );
    event IntentAuthorized(
        bytes32 indexed id,
        address indexed stockToken,
        ISpotExecution.Side side,
        uint256 notional,
        uint256 price,
        uint256 multiplier
    );
    event ExecutionSettled(
        bytes32 indexed id,
        address indexed stockToken,
        uint256 amountIn,
        uint256 amountOut,
        uint256 inventory,
        uint256 grossExposure
    );

    error NotAdmin();
    error NotGuardian();
    error NotExecutor();
    error NotBootstrapper();
    error AlreadyBound();
    error InvalidAddress();
    error InvalidConfiguration();
    error InvalidModeTransition();
    error Halted();
    error ReduceOnly();
    error MarketDisabled(address stockToken);
    error StaleConfiguration(uint64 expected, uint64 actual);
    error InvalidIntent();
    error IntentAlreadyUsed(bytes32 id);
    error DeadlineExpired();
    error DeadlineTooFar();
    error OraclePaused(address stockToken);
    error OracleInvalid(address feed);
    error OracleStale(address feed, uint256 updatedAt);
    error SequencerDown();
    error SequencerGracePeriod();
    error MultiplierTransition(
        uint256 currentMultiplier, uint256 pendingMultiplier, uint256 effectiveAt
    );
    error OrderLimitExceeded(uint256 attempted, uint256 limit);
    error TurnoverLimitExceeded(uint256 attempted, uint256 limit);
    error GrossLimitExceeded(uint256 attempted, uint256 limit);
    error InventoryLimitExceeded(uint256 attempted, uint256 limit);
    error ActiveMarketLimitExceeded(uint256 attempted, uint256 limit);
    error InsufficientInventory(uint256 attempted, uint256 available);
    error SettlementMismatch();

    modifier onlyAdmin() {
        if (msg.sender != admin) revert NotAdmin();
        _;
    }

    modifier onlyExecutor() {
        if (msg.sender != executor) revert NotExecutor();
        _;
    }

    constructor(
        IERC20Metadata settlementAsset_,
        IChainlinkFeed sequencerFeed_,
        address admin_,
        address guardian_,
        address bootstrapper_,
        uint256 grossNotionalLimit_,
        uint256 turnoverLimit_,
        uint64 turnoverWindow_,
        uint64 maxDeadlineDelay_,
        uint64 sequencerGracePeriod_,
        uint8 maxActiveMarkets_
    ) {
        if (
            address(settlementAsset_).code.length == 0 || address(sequencerFeed_).code.length == 0
                || admin_ == address(0) || guardian_ == address(0) || bootstrapper_ == address(0)
        ) revert InvalidAddress();
        if (admin_ == guardian_) revert InvalidConfiguration();

        uint8 decimals_ = settlementAsset_.decimals();
        if (decimals_ > 18) revert InvalidConfiguration();

        settlementAsset = settlementAsset_;
        sequencerFeed = sequencerFeed_;
        admin = admin_;
        guardian = guardian_;
        bootstrapper = bootstrapper_;
        settlementDecimals = decimals_;
        mode = Mode.Halted;
        _setLimits(
            grossNotionalLimit_,
            turnoverLimit_,
            turnoverWindow_,
            maxDeadlineDelay_,
            sequencerGracePeriod_,
            maxActiveMarkets_
        );
    }

    function bindExecutor(address executor_) external {
        if (msg.sender != bootstrapper) revert NotBootstrapper();
        if (executor != address(0)) revert AlreadyBound();
        if (executor_.code.length == 0) revert InvalidAddress();
        executor = executor_;
        delete bootstrapper;
        emit ExecutorBound(executor_);
    }

    function setMode(Mode mode_) external onlyAdmin {
        mode = mode_;
        emit ModeSet(mode_, msg.sender);
    }

    function restrictMode(Mode mode_) external {
        if (msg.sender != guardian) revert NotGuardian();
        if (mode_ == Mode.Active || uint8(mode_) < uint8(mode)) revert InvalidModeTransition();
        mode = mode_;
        emit ModeSet(mode_, msg.sender);
    }

    function setLimits(
        uint256 grossNotionalLimit_,
        uint256 turnoverLimit_,
        uint64 turnoverWindow_,
        uint64 maxDeadlineDelay_,
        uint64 sequencerGracePeriod_,
        uint8 maxActiveMarkets_
    ) external onlyAdmin {
        _setLimits(
            grossNotionalLimit_,
            turnoverLimit_,
            turnoverWindow_,
            maxDeadlineDelay_,
            sequencerGracePeriod_,
            maxActiveMarkets_
        );
    }

    function setMarket(
        address stockToken,
        IChainlinkFeed feed,
        uint128 maxOrderNotional,
        uint128 maxInventory,
        uint64 heartbeat,
        uint64 version,
        bool enabled
    ) external onlyAdmin {
        if (stockToken.code.length == 0 || address(feed).code.length == 0) {
            revert InvalidAddress();
        }
        if (
            maxOrderNotional == 0 || maxInventory == 0 || heartbeat == 0 || version == 0
                || version <= markets[stockToken].version
        ) revert InvalidConfiguration();

        uint8 tokenDecimals = IERC20Metadata(stockToken).decimals();
        uint8 feedDecimals = feed.decimals();
        if (tokenDecimals > 18 || feedDecimals > 18) revert InvalidConfiguration();

        IRobinhoodStockToken token = IRobinhoodStockToken(stockToken);
        if (token.uiMultiplier() == 0 || token.newUIMultiplier() == 0) {
            revert InvalidConfiguration();
        }
        token.effectiveAt();
        token.oraclePaused();

        markets[stockToken] = MarketConfig({
            feed: feed,
            maxOrderNotional: maxOrderNotional,
            maxInventory: maxInventory,
            heartbeat: heartbeat,
            version: version,
            tokenDecimals: tokenDecimals,
            feedDecimals: feedDecimals,
            enabled: enabled
        });
        emit MarketSet(
            stockToken, address(feed), version, maxOrderNotional, maxInventory, heartbeat, enabled
        );
    }

    function authorize(ISpotExecution.SpotIntent calldata intent)
        external
        onlyExecutor
        returns (uint256 notional, uint256 price, uint256 multiplier)
    {
        if (mode == Mode.Halted) revert Halted();
        if (mode == Mode.ReduceOnly && intent.side == ISpotExecution.Side.BuySpot) {
            revert ReduceOnly();
        }
        if (
            intent.id == bytes32(0) || intent.stockToken == address(0) || intent.amountIn == 0
                || intent.minAmountOut == 0 || pending.id != bytes32(0)
        ) revert InvalidIntent();
        if (usedIntent[intent.id]) revert IntentAlreadyUsed(intent.id);
        if (intent.deadline < block.timestamp) revert DeadlineExpired();
        if (intent.deadline > block.timestamp + maxDeadlineDelay) revert DeadlineTooFar();

        MarketConfig memory market = markets[intent.stockToken];
        if (!market.enabled) revert MarketDisabled(intent.stockToken);
        if (intent.configVersion != market.version) {
            revert StaleConfiguration(market.version, intent.configVersion);
        }

        _checkSequencer();
        (price, multiplier) = _readMarket(intent.stockToken, market);
        if (intent.side == ISpotExecution.Side.BuySpot) {
            notional = intent.amountIn;
        } else {
            uint256 available = inventory[intent.stockToken];
            if (intent.amountIn > available) {
                revert InsufficientInventory(intent.amountIn, available);
            }
            notional = _tokenNotional(intent.amountIn, price, market);
        }
        if (notional > market.maxOrderNotional) {
            revert OrderLimitExceeded(notional, market.maxOrderNotional);
        }

        _consumeTurnover(notional);
        usedIntent[intent.id] = true;
        pending = PendingExecution({
            id: intent.id,
            stockToken: intent.stockToken,
            side: intent.side,
            amountIn: intent.amountIn,
            minAmountOut: intent.minAmountOut
        });

        emit IntentAuthorized(
            intent.id, intent.stockToken, intent.side, notional, price, multiplier
        );
    }

    function settle(bytes32 id, uint256 actualIn, uint256 actualOut) external onlyExecutor {
        PendingExecution memory execution = pending;
        if (
            execution.id != id || actualIn != execution.amountIn
                || actualOut < execution.minAmountOut
        ) {
            revert SettlementMismatch();
        }

        MarketConfig memory market = markets[execution.stockToken];
        uint256 currentInventory = inventory[execution.stockToken];
        if (execution.side == ISpotExecution.Side.BuySpot) {
            uint256 nextInventory = currentInventory + actualOut;
            if (nextInventory > market.maxInventory) {
                revert InventoryLimitExceeded(nextInventory, market.maxInventory);
            }

            uint256 nextGross = grossExposure + actualIn;
            if (nextGross > grossNotionalLimit) {
                revert GrossLimitExceeded(nextGross, grossNotionalLimit);
            }
            if (currentInventory == 0) {
                uint256 nextCount = activeMarketCount + 1;
                if (nextCount > maxActiveMarkets) {
                    revert ActiveMarketLimitExceeded(nextCount, maxActiveMarkets);
                }
                activeMarketCount = nextCount;
            }
            inventory[execution.stockToken] = nextInventory;
            inventoryCost[execution.stockToken] += actualIn;
            grossExposure = nextGross;
        } else {
            if (actualIn > currentInventory) {
                revert InsufficientInventory(actualIn, currentInventory);
            }
            uint256 currentCost = inventoryCost[execution.stockToken];
            uint256 costRemoved = actualIn == currentInventory
                ? currentCost
                : Math.mulDiv(currentCost, actualIn, currentInventory);
            inventory[execution.stockToken] = currentInventory - actualIn;
            inventoryCost[execution.stockToken] = currentCost - costRemoved;
            grossExposure -= costRemoved;
            if (actualIn == currentInventory) activeMarketCount -= 1;
        }

        delete pending;
        emit ExecutionSettled(
            id,
            execution.stockToken,
            actualIn,
            actualOut,
            inventory[execution.stockToken],
            grossExposure
        );
    }

    function isMarket(address stockToken) external view returns (bool) {
        return markets[stockToken].enabled;
    }

    function pendingIntent() external view returns (bytes32) {
        return pending.id;
    }

    function _setLimits(
        uint256 grossNotionalLimit_,
        uint256 turnoverLimit_,
        uint64 turnoverWindow_,
        uint64 maxDeadlineDelay_,
        uint64 sequencerGracePeriod_,
        uint8 maxActiveMarkets_
    ) private {
        if (
            grossNotionalLimit_ == 0 || turnoverLimit_ == 0 || turnoverWindow_ == 0
                || maxDeadlineDelay_ == 0 || sequencerGracePeriod_ == 0 || maxActiveMarkets_ == 0
        ) revert InvalidConfiguration();
        grossNotionalLimit = grossNotionalLimit_;
        turnoverLimit = turnoverLimit_;
        turnoverWindow = turnoverWindow_;
        maxDeadlineDelay = maxDeadlineDelay_;
        sequencerGracePeriod = sequencerGracePeriod_;
        maxActiveMarkets = maxActiveMarkets_;
        emit LimitsSet(
            grossNotionalLimit_,
            turnoverLimit_,
            turnoverWindow_,
            maxDeadlineDelay_,
            sequencerGracePeriod_,
            maxActiveMarkets_
        );
    }

    function _consumeTurnover(uint256 notional) private {
        if (block.timestamp >= turnoverWindowStart + turnoverWindow) {
            turnoverWindowStart = block.timestamp;
            windowTurnover = 0;
        }
        uint256 nextTurnover = windowTurnover + notional;
        if (nextTurnover > turnoverLimit) {
            revert TurnoverLimitExceeded(nextTurnover, turnoverLimit);
        }
        windowTurnover = nextTurnover;
    }

    function _checkSequencer() private view {
        (, int256 answer, uint256 startedAt,,) = sequencerFeed.latestRoundData();
        if (answer != 0) revert SequencerDown();
        if (startedAt == 0 || startedAt > block.timestamp) revert SequencerGracePeriod();
        if (block.timestamp - startedAt <= sequencerGracePeriod) revert SequencerGracePeriod();
    }

    function _readMarket(address stockToken, MarketConfig memory market)
        private
        view
        returns (uint256 price, uint256 multiplier)
    {
        IRobinhoodStockToken token = IRobinhoodStockToken(stockToken);
        if (token.oraclePaused()) revert OraclePaused(stockToken);

        multiplier = token.uiMultiplier();
        uint256 nextMultiplier = token.newUIMultiplier();
        uint256 effectiveAt = token.effectiveAt();
        if (multiplier == 0 || nextMultiplier != multiplier) {
            revert MultiplierTransition(multiplier, nextMultiplier, effectiveAt);
        }

        (uint80 roundId, int256 answer,, uint256 updatedAt, uint80 answeredInRound) =
            market.feed.latestRoundData();
        if (
            answer <= 0 || updatedAt == 0 || updatedAt > block.timestamp
                || answeredInRound < roundId
        ) {
            revert OracleInvalid(address(market.feed));
        }
        if (block.timestamp - updatedAt > market.heartbeat) {
            revert OracleStale(address(market.feed), updatedAt);
        }
        price = SafeCast.toUint256(answer);
    }

    function _tokenNotional(uint256 amount, uint256 price, MarketConfig memory market)
        private
        view
        returns (uint256)
    {
        uint256 valueAtFeedDecimals = Math.mulDiv(amount, price, 10 ** market.tokenDecimals);
        return Math.mulDiv(valueAtFeedDecimals, 10 ** settlementDecimals, 10 ** market.feedDecimals);
    }
}
