// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

interface ISpotExecution {
    enum Side {
        BuySpot,
        SellSpot
    }

    struct SpotIntent {
        bytes32 id;
        address stockToken;
        Side side;
        uint128 amountIn;
        uint128 minAmountOut;
        uint64 deadline;
        uint64 configVersion;
    }
}
