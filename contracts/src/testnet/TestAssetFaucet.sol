// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { SafeERC20 } from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";

/// @notice One claim per address for the Robinhood Chain testnet onboarding path.
contract TestAssetFaucet {
    using SafeERC20 for IERC20;

    IERC20 public immutable asset;
    uint256 public immutable claimAmount;
    mapping(address => bool) public claimed;

    event Claimed(address indexed account, uint256 amount);

    error AlreadyClaimed();
    error InvalidConfiguration();

    constructor(IERC20 asset_, uint256 claimAmount_) {
        if (address(asset_) == address(0) || claimAmount_ == 0) revert InvalidConfiguration();
        asset = asset_;
        claimAmount = claimAmount_;
    }

    function claim() external {
        if (claimed[msg.sender]) revert AlreadyClaimed();
        claimed[msg.sender] = true;
        asset.safeTransfer(msg.sender, claimAmount);
        emit Claimed(msg.sender, claimAmount);
    }
}
