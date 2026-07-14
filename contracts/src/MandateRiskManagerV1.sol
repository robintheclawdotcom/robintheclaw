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
        uint16 maxSlippageBps;
        uint8 tokenDecimals;
        uint8 feedDecimals;
        bool entryEnabled;
        bool exitEnabled;
    }

    struct PendingExecution {
        bytes32 id;
        address stockToken;
        ISpotExecution.Side side;
        uint128 amountIn;
        uint128 minAmountOut;
        uint256 expectedUIMultiplier;
        uint80 minOracleRoundId;
    }

    uint16 public constant MAX_SLIPPAGE_BPS = 500;
    uint16 public constant LEGACY_SLIPPAGE_BPS = 100;
    uint256 private constant BPS = 10_000;

    IERC20Metadata public immutable settlementAsset;
    bytes32 public immutable settlementAssetCodeHash;
    IChainlinkFeed public immutable sequencerFeed;
    bytes32 public immutable sequencerFeedCodeHash;
    address public immutable configAdmin;
    address public guardian;
    address public immutable treasury;
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
    uint256 public activeMarketCount;

    PendingExecution private pending;
    mapping(address => MarketConfig) public markets;
    mapping(address => bytes32) public marketTokenCodeHash;
    mapping(address => bytes32) public marketFeedCodeHash;
    mapping(address => uint256) public inventory;
    mapping(address => uint256) public inventoryCost;
    mapping(bytes32 => bool) public usedIntent;
    address[] public activeMarkets;
    mapping(address => uint256) private activeMarketIndexPlusOne;

    event ExecutorBound(address indexed executor);
    event GuardianSet(address indexed previousGuardian, address indexed guardian);
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
        uint16 maxSlippageBps,
        bool entryEnabled,
        bool exitEnabled
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
        uint256 bookCost
    );

    error NotConfigAdmin();
    error NotTreasury();
    error NotRestrictor();
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
    error OracleRoundTooOld(uint80 minimum, uint80 actual);
    error SequencerDown();
    error SequencerGracePeriod();
    error MultiplierTransition(
        uint256 currentMultiplier, uint256 pendingMultiplier, uint256 effectiveAt
    );
    error MultiplierMismatch(uint256 expected, uint256 actual);
    error SlippageLimitExceeded(uint256 minimum, uint256 supplied);
    error OrderLimitExceeded(uint256 attempted, uint256 limit);
    error TurnoverLimitExceeded(uint256 attempted, uint256 limit);
    error GrossLimitExceeded(uint256 attempted, uint256 limit);
    error InventoryLimitExceeded(uint256 attempted, uint256 limit);
    error ActiveMarketLimitExceeded(uint256 attempted, uint256 limit);
    error InsufficientInventory(uint256 attempted, uint256 available);
    error SettlementMismatch();
    error LimitsCanOnlyDecrease();
    error ExternalCodeChanged(address target, bytes32 expected, bytes32 actual);

    modifier onlyConfigAdmin() {
        if (msg.sender != configAdmin) revert NotConfigAdmin();
        _;
    }

    modifier onlyExecutor() {
        if (msg.sender != executor) revert NotExecutor();
        _;
    }

    modifier onlyTreasury() {
        if (msg.sender != treasury) revert NotTreasury();
        _;
    }

    constructor(
        IERC20Metadata settlementAsset_,
        IChainlinkFeed sequencerFeed_,
        address configAdmin_,
        address guardian_,
        address treasury_,
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
                || configAdmin_.code.length == 0 || guardian_ == address(0)
                || treasury_ == address(0) || bootstrapper_ == address(0)
        ) revert InvalidAddress();
        if (
            configAdmin_ == guardian_ || configAdmin_ == treasury_ || guardian_ == treasury_
                || bootstrapper_ == configAdmin_ || bootstrapper_ == guardian_
                || bootstrapper_ == treasury_
        ) revert InvalidConfiguration();

        uint8 decimals_ = settlementAsset_.decimals();
        if (decimals_ > 18) revert InvalidConfiguration();

        settlementAsset = settlementAsset_;
        settlementAssetCodeHash = address(settlementAsset_).codehash;
        sequencerFeed = sequencerFeed_;
        sequencerFeedCodeHash = address(sequencerFeed_).codehash;
        configAdmin = configAdmin_;
        guardian = guardian_;
        treasury = treasury_;
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

    function setMode(Mode mode_) external onlyConfigAdmin {
        mode = mode_;
        emit ModeSet(mode_, msg.sender);
    }

    function haltFromExecutor() external onlyExecutor {
        mode = Mode.Halted;
        emit ModeSet(Mode.Halted, msg.sender);
    }

    function setGuardian(address guardian_) external onlyConfigAdmin {
        if (guardian_ == address(0)) revert InvalidAddress();
        if (guardian_ == configAdmin || guardian_ == treasury) revert InvalidConfiguration();
        address previous = guardian;
        guardian = guardian_;
        emit GuardianSet(previous, guardian_);
    }

    function restrictMode(Mode mode_) external {
        if (msg.sender != guardian && msg.sender != treasury) revert NotRestrictor();
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
    ) external onlyConfigAdmin {
        _setLimits(
            grossNotionalLimit_,
            turnoverLimit_,
            turnoverWindow_,
            maxDeadlineDelay_,
            sequencerGracePeriod_,
            maxActiveMarkets_
        );
    }

    function lowerLimits(uint256 grossNotionalLimit_, uint256 turnoverLimit_)
        external
        onlyTreasury
    {
        if (
            grossNotionalLimit_ > grossNotionalLimit || turnoverLimit_ > turnoverLimit
                || grossNotionalLimit_ == 0 || turnoverLimit_ == 0
        ) revert LimitsCanOnlyDecrease();
        _setLimits(
            grossNotionalLimit_,
            turnoverLimit_,
            turnoverWindow,
            maxDeadlineDelay,
            sequencerGracePeriod,
            maxActiveMarkets
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
    ) external onlyConfigAdmin {
        _setMarket(
            stockToken,
            feed,
            maxOrderNotional,
            maxInventory,
            heartbeat,
            version,
            LEGACY_SLIPPAGE_BPS,
            enabled,
            enabled
        );
    }

    function setMarket(
        address stockToken,
        IChainlinkFeed feed,
        uint128 maxOrderNotional,
        uint128 maxInventory,
        uint64 heartbeat,
        uint64 version,
        uint16 maxSlippageBps,
        bool entryEnabled,
        bool exitEnabled
    ) external onlyConfigAdmin {
        _setMarket(
            stockToken,
            feed,
            maxOrderNotional,
            maxInventory,
            heartbeat,
            version,
            maxSlippageBps,
            entryEnabled,
            exitEnabled
        );
    }

    function authorize(ISpotExecution.SpotIntent calldata intent)
        external
        onlyExecutor
        returns (uint256 notional, uint256 price, uint256 multiplier)
    {
        _checkCode(address(settlementAsset), settlementAssetCodeHash);
        if (mode == Mode.Halted) revert Halted();
        if (intent.side == ISpotExecution.Side.BuySpot && mode != Mode.Active) revert ReduceOnly();
        if (
            intent.id == bytes32(0) || intent.stockToken == address(0) || intent.amountIn == 0
                || intent.minAmountOut == 0 || intent.expectedUIMultiplier == 0
                || intent.minOracleRoundId == 0 || pending.id != bytes32(0)
        ) revert InvalidIntent();
        if (usedIntent[intent.id]) revert IntentAlreadyUsed(intent.id);
        if (intent.deadline < block.timestamp) revert DeadlineExpired();
        if (intent.deadline > block.timestamp + maxDeadlineDelay) revert DeadlineTooFar();

        MarketConfig memory market = markets[intent.stockToken];
        bool enabled =
            intent.side == ISpotExecution.Side.BuySpot ? market.entryEnabled : market.exitEnabled;
        if (!enabled) revert MarketDisabled(intent.stockToken);
        if (intent.configVersion != market.version) {
            revert StaleConfiguration(market.version, intent.configVersion);
        }

        _checkSequencer();
        uint80 roundId;
        (price, multiplier, roundId) = _readMarket(intent.stockToken, market);
        if (roundId < intent.minOracleRoundId) {
            revert OracleRoundTooOld(intent.minOracleRoundId, roundId);
        }
        if (multiplier != intent.expectedUIMultiplier) {
            revert MultiplierMismatch(intent.expectedUIMultiplier, multiplier);
        }

        if (intent.side == ISpotExecution.Side.BuySpot) {
            notional = intent.amountIn;
            if (inventory[intent.stockToken] == 0 && activeMarketCount >= maxActiveMarkets) {
                revert ActiveMarketLimitExceeded(activeMarketCount + 1, maxActiveMarkets);
            }
            uint256 currentGross = _currentGrossNotional(address(0), 0, 0, 0);
            uint256 projectedGross = currentGross + notional;
            if (projectedGross > grossNotionalLimit) {
                revert GrossLimitExceeded(projectedGross, grossNotionalLimit);
            }
            _consumeTurnover(notional);
        } else {
            uint256 available = inventory[intent.stockToken];
            if (intent.amountIn > available) {
                revert InsufficientInventory(intent.amountIn, available);
            }
            notional = intent.amountIn == available
                ? inventoryCost[intent.stockToken]
                : _tokenNotional(intent.amountIn, price, multiplier, market, Math.Rounding.Ceil);
            _consumeTurnover(notional);
        }
        if (notional > market.maxOrderNotional) {
            revert OrderLimitExceeded(notional, market.maxOrderNotional);
        }
        _checkMinimumOutput(intent, price, multiplier, market);

        usedIntent[intent.id] = true;
        pending = PendingExecution({
            id: intent.id,
            stockToken: intent.stockToken,
            side: intent.side,
            amountIn: intent.amountIn,
            minAmountOut: intent.minAmountOut,
            expectedUIMultiplier: intent.expectedUIMultiplier,
            minOracleRoundId: intent.minOracleRoundId
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
        ) revert SettlementMismatch();

        MarketConfig memory market = markets[execution.stockToken];
        uint256 currentInventory = inventory[execution.stockToken];
        if (execution.side == ISpotExecution.Side.BuySpot) {
            uint256 nextInventory = currentInventory + actualOut;
            if (nextInventory > market.maxInventory) {
                revert InventoryLimitExceeded(nextInventory, market.maxInventory);
            }

            _checkSequencer();
            (uint256 price, uint256 multiplier, uint80 roundId) =
                _readMarket(execution.stockToken, market);
            if (roundId < execution.minOracleRoundId) {
                revert OracleRoundTooOld(execution.minOracleRoundId, roundId);
            }
            if (multiplier != execution.expectedUIMultiplier) {
                revert MultiplierMismatch(execution.expectedUIMultiplier, multiplier);
            }
            uint256 nextGross =
                _currentGrossNotional(execution.stockToken, nextInventory, price, multiplier);
            if (nextGross > grossNotionalLimit) {
                revert GrossLimitExceeded(nextGross, grossNotionalLimit);
            }
            if (currentInventory == 0) _addActiveMarket(execution.stockToken);
            inventory[execution.stockToken] = nextInventory;
            inventoryCost[execution.stockToken] += actualIn;
        } else {
            if (actualIn > currentInventory) {
                revert InsufficientInventory(actualIn, currentInventory);
            }
            uint256 currentCost = inventoryCost[execution.stockToken];
            uint256 costRemoved = actualIn == currentInventory
                ? currentCost
                : Math.mulDiv(currentCost, actualIn, currentInventory);
            uint256 nextInventory = currentInventory - actualIn;
            inventory[execution.stockToken] = nextInventory;
            inventoryCost[execution.stockToken] = currentCost - costRemoved;
            if (nextInventory == 0) _removeActiveMarket(execution.stockToken);
        }

        delete pending;
        emit ExecutionSettled(
            id,
            execution.stockToken,
            actualIn,
            actualOut,
            inventory[execution.stockToken],
            inventoryCost[execution.stockToken]
        );
    }

    function isMarket(address stockToken) external view returns (bool) {
        MarketConfig storage market = markets[stockToken];
        return market.entryEnabled || market.exitEnabled;
    }

    function pendingIntent() external view returns (bytes32) {
        return pending.id;
    }

    function activeMarketAt(uint256 index) external view returns (address) {
        return activeMarkets[index];
    }

    function grossExposure() public view returns (uint256) {
        if (activeMarketCount == 0) return 0;
        _checkSequencer();
        return _currentGrossNotional(address(0), 0, 0, 0);
    }

    function currentGrossNotional() external view returns (uint256) {
        return grossExposure();
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
                || maxActiveMarkets_ < activeMarketCount
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

    function _setMarket(
        address stockToken,
        IChainlinkFeed feed,
        uint128 maxOrderNotional,
        uint128 maxInventory,
        uint64 heartbeat,
        uint64 version,
        uint16 maxSlippageBps,
        bool entryEnabled,
        bool exitEnabled
    ) private {
        if (stockToken.code.length == 0 || address(feed).code.length == 0) {
            revert InvalidAddress();
        }
        if (
            maxOrderNotional == 0 || maxInventory == 0 || heartbeat == 0 || version == 0
                || version <= markets[stockToken].version || maxSlippageBps > MAX_SLIPPAGE_BPS
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
            maxSlippageBps: maxSlippageBps,
            tokenDecimals: tokenDecimals,
            feedDecimals: feedDecimals,
            entryEnabled: entryEnabled,
            exitEnabled: exitEnabled
        });
        marketTokenCodeHash[stockToken] = stockToken.codehash;
        marketFeedCodeHash[stockToken] = address(feed).codehash;
        emit MarketSet(
            stockToken,
            address(feed),
            version,
            maxOrderNotional,
            maxInventory,
            heartbeat,
            maxSlippageBps,
            entryEnabled,
            exitEnabled
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
        _checkCode(address(sequencerFeed), sequencerFeedCodeHash);
        (, int256 answer, uint256 startedAt,,) = sequencerFeed.latestRoundData();
        if (answer != 0) revert SequencerDown();
        if (startedAt == 0 || startedAt > block.timestamp) revert SequencerGracePeriod();
        if (block.timestamp - startedAt <= sequencerGracePeriod) revert SequencerGracePeriod();
    }

    function _readMarket(address stockToken, MarketConfig memory market)
        private
        view
        returns (uint256 price, uint256 multiplier, uint80 roundId)
    {
        IRobinhoodStockToken token = IRobinhoodStockToken(stockToken);
        _checkCode(stockToken, marketTokenCodeHash[stockToken]);
        if (token.oraclePaused()) revert OraclePaused(stockToken);

        _checkCode(address(market.feed), marketFeedCodeHash[stockToken]);

        multiplier = token.uiMultiplier();
        uint256 nextMultiplier = token.newUIMultiplier();
        uint256 effectiveAt = token.effectiveAt();
        if (multiplier == 0 || nextMultiplier != multiplier) {
            revert MultiplierTransition(multiplier, nextMultiplier, effectiveAt);
        }

        int256 answer;
        uint256 updatedAt;
        uint80 answeredInRound;
        (roundId, answer,, updatedAt, answeredInRound) = market.feed.latestRoundData();
        if (
            roundId == 0 || answer <= 0 || updatedAt == 0 || updatedAt > block.timestamp
                || answeredInRound < roundId
        ) revert OracleInvalid(address(market.feed));
        if (block.timestamp - updatedAt > market.heartbeat) {
            revert OracleStale(address(market.feed), updatedAt);
        }
        price = SafeCast.toUint256(answer);
    }

    function _checkCode(address target, bytes32 expected) private view {
        bytes32 actual = target.codehash;
        if (actual != expected) revert ExternalCodeChanged(target, expected, actual);
    }

    function _checkMinimumOutput(
        ISpotExecution.SpotIntent calldata intent,
        uint256 price,
        uint256 multiplier,
        MarketConfig memory market
    ) private view {
        uint256 retainedBps = BPS - market.maxSlippageBps;
        uint256 minimum;
        uint256 supplied;
        if (intent.side == ISpotExecution.Side.BuySpot) {
            minimum = Math.mulDiv(intent.amountIn, retainedBps, BPS, Math.Rounding.Ceil);
            supplied = _tokenNotional(
                intent.minAmountOut, price, multiplier, market, Math.Rounding.Floor
            );
        } else {
            uint256 inputNotional =
                _tokenNotional(intent.amountIn, price, multiplier, market, Math.Rounding.Ceil);
            minimum = Math.mulDiv(inputNotional, retainedBps, BPS, Math.Rounding.Ceil);
            supplied = intent.minAmountOut;
        }
        if (supplied < minimum) revert SlippageLimitExceeded(minimum, supplied);
    }

    function _currentGrossNotional(
        address overrideMarket,
        uint256 overrideInventory,
        uint256 overridePrice,
        uint256 overrideMultiplier
    ) private view returns (uint256 gross) {
        uint256 length = activeMarkets.length;
        bool foundOverride;
        for (uint256 i; i < length; ++i) {
            address stockToken = activeMarkets[i];
            MarketConfig memory market = markets[stockToken];
            uint256 balance = inventory[stockToken];
            uint256 price;
            uint256 multiplier;
            if (stockToken == overrideMarket) {
                foundOverride = true;
                balance = overrideInventory;
                price = overridePrice;
                multiplier = overrideMultiplier;
            } else {
                (price, multiplier,) = _readMarket(stockToken, market);
            }
            gross += _tokenNotional(balance, price, multiplier, market, Math.Rounding.Ceil);
        }
        if (overrideMarket != address(0) && !foundOverride) {
            gross += _tokenNotional(
                overrideInventory,
                overridePrice,
                overrideMultiplier,
                markets[overrideMarket],
                Math.Rounding.Ceil
            );
        }
    }

    function _tokenNotional(
        uint256 amount,
        uint256 price,
        uint256 multiplier,
        MarketConfig memory market,
        Math.Rounding rounding
    ) private view returns (uint256) {
        uint256 adjustedAmount = Math.mulDiv(amount, multiplier, 1e18, rounding);
        uint256 valueAtFeedDecimals =
            Math.mulDiv(adjustedAmount, price, 10 ** market.tokenDecimals, rounding);
        return Math.mulDiv(
            valueAtFeedDecimals, 10 ** settlementDecimals, 10 ** market.feedDecimals, rounding
        );
    }

    function _addActiveMarket(address stockToken) private {
        if (activeMarketIndexPlusOne[stockToken] != 0) return;
        uint256 nextCount = activeMarkets.length + 1;
        if (nextCount > maxActiveMarkets) {
            revert ActiveMarketLimitExceeded(nextCount, maxActiveMarkets);
        }
        activeMarkets.push(stockToken);
        activeMarketIndexPlusOne[stockToken] = nextCount;
        activeMarketCount = nextCount;
    }

    function _removeActiveMarket(address stockToken) private {
        uint256 indexPlusOne = activeMarketIndexPlusOne[stockToken];
        if (indexPlusOne == 0) return;
        uint256 index = indexPlusOne - 1;
        uint256 lastIndex = activeMarkets.length - 1;
        if (index != lastIndex) {
            address lastMarket = activeMarkets[lastIndex];
            activeMarkets[index] = lastMarket;
            activeMarketIndexPlusOne[lastMarket] = index + 1;
        }
        activeMarkets.pop();
        delete activeMarketIndexPlusOne[stockToken];
        activeMarketCount = activeMarkets.length;
    }
}
