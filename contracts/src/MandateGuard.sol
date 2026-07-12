// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

/// @title MandateGuard
/// @notice The on-chain mandate the agent cannot exceed. Every execution consults check()
///         first: a call to a non-allowlisted (target, selector), while halted, or that pushes
///         cumulative notional past the per-window cap reverts. The window rolls lazily. Only
///         the executor (the vault) may call check(); only the owner (human boundary) can
///         change the mandate or pull the kill switch.
contract MandateGuard {
    address public immutable owner;
    address public executor;

    uint256 public windowNotionalCap;
    uint64 public windowLength;
    bool public halted;

    uint256 public windowStart;
    uint256 public windowSpent;

    mapping(address => mapping(bytes4 => bool)) public allowed;

    event ExecutorSet(address indexed executor);
    event TargetAllowed(address indexed target, bytes4 indexed selector, bool ok);
    event CapUpdated(uint256 cap, uint64 window);
    event HaltSet(bool halted);
    event Checked(
        address indexed target, bytes4 indexed selector, uint256 notional, uint256 windowSpent
    );

    error NotOwner();
    error NotExecutor();
    error NotAllowed(address target, bytes4 selector);
    error CapExceeded(uint256 attempted, uint256 cap);
    error IsHalted();

    modifier onlyOwner() {
        if (msg.sender != owner) revert NotOwner();
        _;
    }

    constructor(address owner_, address executor_, uint256 cap_, uint64 window_) {
        require(owner_ != address(0) && executor_ != address(0), "zero addr");
        require(window_ > 0, "window=0");
        owner = owner_;
        executor = executor_;
        windowNotionalCap = cap_;
        windowLength = window_;
        windowStart = block.timestamp;
    }

    function setExecutor(address executor_) external onlyOwner {
        require(executor_ != address(0), "zero addr");
        executor = executor_;
        emit ExecutorSet(executor_);
    }

    function setAllowed(address target, bytes4 selector, bool ok) external onlyOwner {
        allowed[target][selector] = ok;
        emit TargetAllowed(target, selector, ok);
    }

    function setCap(uint256 cap_, uint64 window_) external onlyOwner {
        require(window_ > 0, "window=0");
        windowNotionalCap = cap_;
        windowLength = window_;
        emit CapUpdated(cap_, window_);
    }

    function setHalted(bool h) external onlyOwner {
        halted = h;
        emit HaltSet(h);
    }

    /// @notice Reverts unless (target, selector) is allowlisted, not halted, and the rolling
    ///         window notional stays within cap. Accrues notional, so the vault must call this
    ///         atomically with the execution it authorizes.
    function check(address target, bytes4 selector, uint256 notional) external returns (bool) {
        if (msg.sender != executor) revert NotExecutor();
        if (halted) revert IsHalted();
        if (!allowed[target][selector]) revert NotAllowed(target, selector);

        if (block.timestamp >= windowStart + windowLength) {
            windowStart = block.timestamp;
            windowSpent = 0;
        }

        uint256 attempted = windowSpent + notional;
        if (attempted > windowNotionalCap) revert CapExceeded(attempted, windowNotionalCap);
        windowSpent = attempted;

        emit Checked(target, selector, notional, attempted);
        return true;
    }

    function remaining() external view returns (uint256) {
        if (block.timestamp >= windowStart + windowLength) return windowNotionalCap;
        return windowSpent >= windowNotionalCap ? 0 : windowNotionalCap - windowSpent;
    }
}
