// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { SafeERC20 } from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import { AttestationAnchor } from "./AttestationAnchor.sol";
import { MandateGuard } from "./MandateGuard.sol";

/// @notice A single-owner strategy vault with its execution guard and record anchor wired at
/// construction. The owner controls capital and policy; the agent can only execute allowed calls.
contract PersonalStrategyVault {
    using SafeERC20 for IERC20;

    IERC20 public immutable asset;
    MandateGuard public immutable guard;
    address public immutable owner;
    address public agent;
    AttestationAnchor public immutable attestationAnchor;

    event AgentSet(address indexed agent);
    event Deposited(address indexed from, uint256 amount);
    event Withdrawn(address indexed to, uint256 amount);
    event Executed(address indexed target, bytes4 indexed selector, uint256 notional);
    event BatchAnchored(bytes32 indexed root, uint64 indexed sequence, uint64 tradeCount);

    error NotOwner();
    error NotAgent();
    error CallFailed(bytes reason);
    error InvalidAmount();
    error InvalidCalldata();
    error InvalidTarget();

    modifier onlyOwner() {
        if (msg.sender != owner) revert NotOwner();
        _;
    }

    constructor(IERC20 asset_, address owner_, address agent_, uint256 cap_, uint64 window_) {
        if (address(asset_) == address(0) || owner_ == address(0) || agent_ == address(0)) {
            revert InvalidTarget();
        }
        if (cap_ == 0 || window_ == 0) revert InvalidAmount();

        asset = asset_;
        owner = owner_;
        agent = agent_;
        guard = new MandateGuard(owner_, address(this), cap_, window_, true);
        attestationAnchor = new AttestationAnchor(address(this));
    }

    function setAgent(address agent_) external onlyOwner {
        if (agent_ == address(0)) revert InvalidTarget();
        agent = agent_;
        emit AgentSet(agent_);
    }

    function deposit(uint256 amount) external {
        if (amount == 0) revert InvalidAmount();
        asset.safeTransferFrom(msg.sender, address(this), amount);
        emit Deposited(msg.sender, amount);
    }

    function withdraw(address to, uint256 amount) external onlyOwner {
        if (to == address(0)) revert InvalidTarget();
        if (amount == 0) revert InvalidAmount();
        asset.safeTransfer(to, amount);
        emit Withdrawn(to, amount);
    }

    function execute(address target, bytes calldata data, uint256 notional)
        external
        returns (bytes memory)
    {
        if (msg.sender != agent) revert NotAgent();
        if (data.length < 4) revert InvalidCalldata();
        if (target.code.length == 0) revert InvalidTarget();

        bytes4 selector = bytes4(data);
        guard.check(target, selector, notional);
        (bool ok, bytes memory result) = target.call(data);
        if (!ok) revert CallFailed(result);

        emit Executed(target, selector, notional);
        return result;
    }

    function anchorBatch(bytes32 root, uint64 sequence, uint64 tradeCount) external {
        if (msg.sender != agent) revert NotAgent();
        attestationAnchor.anchor(root, sequence, tradeCount);
        emit BatchAnchored(root, sequence, tradeCount);
    }

    function balance() external view returns (uint256) {
        return asset.balanceOf(address(this));
    }
}
