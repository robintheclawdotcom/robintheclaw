// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { SafeERC20 } from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import { MandateGuard } from "./MandateGuard.sol";

/// @title StrategyVault
/// @notice Custodies USDG and executes the agent's trades, but only ones the MandateGuard
///         approves. The agent key can call execute(); it can never move funds to a target or
///         selector outside the mandate, nor past the per-window notional cap. The owner (the
///         human boundary) funds, defunds, rotates the agent, and can halt the guard.
///
///         Share/NAV accounting is intentionally omitted until positions can be priced: this v0
///         is a single-owner custody + guarded-execution boundary, not a public deposit vault.
contract StrategyVault {
    using SafeERC20 for IERC20;

    IERC20 public immutable asset;
    MandateGuard public immutable guard;
    address public immutable owner;
    address public agent;

    event AgentSet(address indexed agent);
    event Funded(address indexed from, uint256 amount);
    event Defunded(address indexed to, uint256 amount);
    event Executed(address indexed target, bytes4 indexed selector, uint256 notional);

    error NotOwner();
    error NotAgent();
    error CallFailed(bytes reason);

    modifier onlyOwner() {
        if (msg.sender != owner) revert NotOwner();
        _;
    }

    constructor(IERC20 asset_, MandateGuard guard_, address owner_, address agent_) {
        require(address(asset_) != address(0) && address(guard_) != address(0), "zero addr");
        require(owner_ != address(0) && agent_ != address(0), "zero addr");
        asset = asset_;
        guard = guard_;
        owner = owner_;
        agent = agent_;
    }

    function setAgent(address agent_) external onlyOwner {
        require(agent_ != address(0), "zero addr");
        agent = agent_;
        emit AgentSet(agent_);
    }

    function fund(uint256 amount) external onlyOwner {
        asset.safeTransferFrom(msg.sender, address(this), amount);
        emit Funded(msg.sender, amount);
    }

    function defund(address to, uint256 amount) external onlyOwner {
        asset.safeTransfer(to, amount);
        emit Defunded(to, amount);
    }

    /// @notice Agent-only. Runs target.call(data) only after the guard approves
    ///         (target, selector, notional). The guard accrues the notional atomically, so a
    ///         call that would breach the mandate reverts before any funds move.
    function execute(address target, bytes calldata data, uint256 notional)
        external
        returns (bytes memory)
    {
        if (msg.sender != agent) revert NotAgent();
        bytes4 selector = bytes4(data);
        guard.check(target, selector, notional);
        (bool ok, bytes memory result) = target.call(data);
        if (!ok) revert CallFailed(result);
        emit Executed(target, selector, notional);
        return result;
    }

    function balance() external view returns (uint256) {
        return asset.balanceOf(address(this));
    }
}
