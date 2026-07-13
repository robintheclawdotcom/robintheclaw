// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { ISpotExecution } from "./ISpotExecution.sol";

interface ISpotAdapter {
    function executeSpot(ISpotExecution.SpotIntent calldata intent)
        external
        returns (uint256 amountOut);
}
