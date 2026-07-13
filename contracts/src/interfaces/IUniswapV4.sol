// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

interface IUniversalRouter {
    function execute(bytes calldata commands, bytes[] calldata inputs, uint256 deadline) external;
}

interface IPermit2AllowanceTransfer {
    function approve(address token, address spender, uint160 amount, uint48 expiration) external;
}

struct PoolKey {
    address currency0;
    address currency1;
    uint24 fee;
    int24 tickSpacing;
    address hooks;
}

struct ExactInputSingleParams {
    PoolKey poolKey;
    bool zeroForOne;
    uint128 amountIn;
    uint128 amountOutMinimum;
    uint256 minHopPriceX36;
    bytes hookData;
}
