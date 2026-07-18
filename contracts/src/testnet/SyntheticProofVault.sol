// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { SafeERC20 } from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import { AttestationAnchor } from "../AttestationAnchor.sol";

/// @notice Non-trading custody used only by the synthetic testnet attestation proof.
contract SyntheticProofVault {
    using SafeERC20 for IERC20;

    IERC20 public immutable asset;
    address public immutable owner;
    address public immutable agent;
    AttestationAnchor public immutable anchor;

    error Unauthorized();

    constructor(IERC20 asset_, address owner_, address agent_) {
        require(address(asset_) != address(0) && owner_ != address(0) && agent_ != address(0));
        asset = asset_;
        owner = owner_;
        agent = agent_;
        anchor = new AttestationAnchor(address(this));
    }

    function fund(uint256 amount) external {
        if (msg.sender != owner) revert Unauthorized();
        // The only caller and transfer source are the immutable testnet owner.
        // slither-disable-next-line arbitrary-send-erc20
        asset.safeTransferFrom(owner, address(this), amount);
    }

    function defund(uint256 amount) external {
        if (msg.sender != owner) revert Unauthorized();
        asset.safeTransfer(owner, amount);
    }

    function anchorBatch(bytes32 root, uint64 sequence, uint64 tradeCount) external {
        if (msg.sender != agent) revert Unauthorized();
        anchor.anchor(root, sequence, tradeCount);
    }
}
