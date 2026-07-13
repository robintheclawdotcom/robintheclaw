// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

interface IRobinhoodStockToken {
    function uiMultiplier() external view returns (uint256);
    function newUIMultiplier() external view returns (uint256);
    function effectiveAt() external view returns (uint256);
    function oraclePaused() external view returns (bool);
}
