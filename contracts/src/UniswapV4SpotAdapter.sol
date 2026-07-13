// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { SafeERC20 } from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import { Math } from "@openzeppelin/contracts/utils/math/Math.sol";
import { ISpotAdapter } from "./interfaces/ISpotAdapter.sol";
import { ISpotExecution } from "./interfaces/ISpotExecution.sol";
import {
    ExactInputSingleParams,
    IPermit2AllowanceTransfer,
    IUniversalRouter,
    PoolKey
} from "./interfaces/IUniswapV4.sol";

contract UniswapV4SpotAdapter is ISpotAdapter {
    using SafeERC20 for IERC20;

    struct MarketRoute {
        PoolKey poolKey;
        uint64 version;
        bool enabled;
    }

    bytes1 private constant V4_SWAP = 0x10;
    bytes1 private constant SWAP_EXACT_IN_SINGLE = 0x06;
    bytes1 private constant SETTLE_ALL = 0x0c;
    bytes1 private constant TAKE_ALL = 0x0f;
    uint256 private constant PRICE_SCALE = 1e36;

    IERC20 public immutable settlementAsset;
    IUniversalRouter public immutable router;
    IPermit2AllowanceTransfer public immutable permit2;
    address public immutable admin;
    bytes32 public immutable routerCodeHash;
    bytes32 public immutable permit2CodeHash;

    address public vault;
    address private bootstrapper;
    mapping(address => MarketRoute) private routes;

    event VaultBound(address indexed vault);
    event MarketSet(
        address indexed stockToken,
        uint64 indexed version,
        address currency0,
        address currency1,
        uint24 fee,
        int24 tickSpacing,
        bool enabled
    );
    event SwapExecuted(
        bytes32 indexed id,
        address indexed stockToken,
        ISpotExecution.Side side,
        uint256 amountIn,
        uint256 amountOut
    );

    error NotAdmin();
    error NotVault();
    error NotBootstrapper();
    error AlreadyBound();
    error InvalidAddress();
    error InvalidRoute();
    error InvalidConfiguration();
    error MarketDisabled(address stockToken);
    error StaleConfiguration(uint64 expected, uint64 actual);
    error InvalidBalanceDelta();
    error InvalidDeadline();
    error ExternalCodeChanged(address target, bytes32 expected, bytes32 actual);

    modifier onlyAdmin() {
        if (msg.sender != admin) revert NotAdmin();
        _;
    }

    constructor(
        IERC20 settlementAsset_,
        IUniversalRouter router_,
        IPermit2AllowanceTransfer permit2_,
        address admin_,
        address bootstrapper_,
        bytes32 routerCodeHash_,
        bytes32 permit2CodeHash_
    ) {
        if (
            address(settlementAsset_).code.length == 0 || address(router_).code.length == 0
                || address(permit2_).code.length == 0 || admin_ == address(0)
                || bootstrapper_ == address(0)
        ) revert InvalidAddress();
        if (
            routerCodeHash_ == bytes32(0) || permit2CodeHash_ == bytes32(0)
                || address(router_).codehash != routerCodeHash_
                || address(permit2_).codehash != permit2CodeHash_
        ) revert InvalidConfiguration();
        settlementAsset = settlementAsset_;
        router = router_;
        permit2 = permit2_;
        admin = admin_;
        bootstrapper = bootstrapper_;
        routerCodeHash = routerCodeHash_;
        permit2CodeHash = permit2CodeHash_;
    }

    function bindVault(address vault_) external {
        if (msg.sender != bootstrapper) revert NotBootstrapper();
        if (vault != address(0)) revert AlreadyBound();
        if (vault_.code.length == 0) revert InvalidAddress();
        vault = vault_;
        delete bootstrapper;
        emit VaultBound(vault_);
    }

    function setMarket(address stockToken, PoolKey calldata poolKey, uint64 version, bool enabled)
        external
        onlyAdmin
    {
        if (stockToken.code.length == 0) revert InvalidAddress();
        MarketRoute storage current = routes[stockToken];
        if (version == 0 || version <= current.version) revert InvalidConfiguration();
        bool validPair =
            (poolKey.currency0 == address(settlementAsset) && poolKey.currency1 == stockToken)
                || (poolKey.currency1 == address(settlementAsset)
                    && poolKey.currency0 == stockToken);
        if (!validPair || poolKey.hooks != address(0) || poolKey.tickSpacing == 0) {
            revert InvalidRoute();
        }
        routes[stockToken] = MarketRoute({ poolKey: poolKey, version: version, enabled: enabled });
        emit MarketSet(
            stockToken,
            version,
            poolKey.currency0,
            poolKey.currency1,
            poolKey.fee,
            poolKey.tickSpacing,
            enabled
        );
    }

    function marketRoute(address stockToken) external view returns (MarketRoute memory) {
        return routes[stockToken];
    }

    function executeSpot(ISpotExecution.SpotIntent calldata intent)
        external
        returns (uint256 amountOut)
    {
        if (msg.sender != vault) revert NotVault();
        if (intent.deadline > type(uint48).max) revert InvalidDeadline();
        _checkCode(address(router), routerCodeHash);
        _checkCode(address(permit2), permit2CodeHash);
        MarketRoute memory route = routes[intent.stockToken];
        if (!route.enabled) revert MarketDisabled(intent.stockToken);
        if (intent.configVersion != route.version) {
            revert StaleConfiguration(route.version, intent.configVersion);
        }

        IERC20 input = intent.side == ISpotExecution.Side.BuySpot
            ? settlementAsset
            : IERC20(intent.stockToken);
        IERC20 output = intent.side == ISpotExecution.Side.BuySpot
            ? IERC20(intent.stockToken)
            : settlementAsset;
        uint256 inputBefore = input.balanceOf(address(this));
        uint256 outputBefore = output.balanceOf(address(this));

        input.safeTransferFrom(vault, address(this), intent.amountIn);
        if (input.balanceOf(address(this)) - inputBefore != intent.amountIn) {
            revert InvalidBalanceDelta();
        }
        input.forceApprove(address(permit2), intent.amountIn);
        permit2.approve(
            address(input), address(router), uint160(intent.amountIn), uint48(intent.deadline)
        );

        _executeRouter(intent, route.poolKey, address(input), address(output));

        permit2.approve(address(input), address(router), 0, 0);
        input.forceApprove(address(permit2), 0);
        if (input.balanceOf(address(this)) != inputBefore) revert InvalidBalanceDelta();

        amountOut = output.balanceOf(address(this)) - outputBefore;
        if (amountOut < intent.minAmountOut) revert InvalidBalanceDelta();
        output.safeTransfer(vault, amountOut);
        if (output.balanceOf(address(this)) != outputBefore) revert InvalidBalanceDelta();

        emit SwapExecuted(intent.id, intent.stockToken, intent.side, intent.amountIn, amountOut);
    }

    function _checkCode(address target, bytes32 expected) private view {
        bytes32 actual = target.codehash;
        if (actual != expected) revert ExternalCodeChanged(target, expected, actual);
    }

    function _executeRouter(
        ISpotExecution.SpotIntent calldata intent,
        PoolKey memory poolKey,
        address input,
        address output
    ) private {
        bool zeroForOne = poolKey.currency0 == input;
        ExactInputSingleParams memory swap = ExactInputSingleParams({
            poolKey: poolKey,
            zeroForOne: zeroForOne,
            amountIn: intent.amountIn,
            amountOutMinimum: intent.minAmountOut,
            minHopPriceX36: Math.mulDiv(intent.minAmountOut, PRICE_SCALE, intent.amountIn),
            hookData: bytes("")
        });

        bytes[] memory params = new bytes[](3);
        params[0] = abi.encode(swap);
        params[1] = abi.encode(input, uint256(intent.amountIn));
        params[2] = abi.encode(output, uint256(intent.minAmountOut));

        bytes[] memory inputs = new bytes[](1);
        inputs[0] = abi.encode(abi.encodePacked(SWAP_EXACT_IN_SINGLE, SETTLE_ALL, TAKE_ALL), params);
        router.execute(abi.encodePacked(V4_SWAP), inputs, intent.deadline);
    }
}
