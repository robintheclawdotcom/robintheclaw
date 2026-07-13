// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { SafeERC20 } from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import { ReentrancyGuard } from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import { AttestationAnchor } from "./AttestationAnchor.sol";
import { MandateRiskManagerV1 } from "./MandateRiskManagerV1.sol";
import { ISpotAdapter } from "./interfaces/ISpotAdapter.sol";
import { ISpotExecution } from "./interfaces/ISpotExecution.sol";

contract RwaStrategyVault is ISpotExecution, ReentrancyGuard {
    using SafeERC20 for IERC20;

    IERC20 public immutable settlementAsset;
    MandateRiskManagerV1 public immutable riskManager;
    ISpotAdapter public immutable spotAdapter;
    AttestationAnchor public immutable attestationAnchor;
    address public immutable admin;
    address public immutable recoveryRecipient;
    address public agent;
    bool public recoveryFinalized;

    event AgentSet(address indexed agent);
    event Deposited(address indexed token, uint256 amount);
    event Recovered(address indexed token, uint256 amount);
    event SpotExecuted(
        bytes32 indexed id,
        address indexed stockToken,
        Side side,
        uint256 amountIn,
        uint256 amountOut,
        uint256 notional,
        uint256 price,
        uint256 multiplier
    );
    event BatchAnchored(bytes32 indexed root, uint64 indexed sequence, uint64 tradeCount);

    error NotAdmin();
    error NotAgent();
    error InvalidAddress();
    error UnsupportedToken();
    error RecoveryRequiresHalt();
    error RecoveryFinalized();
    error InvalidBalanceDelta();

    modifier onlyAdmin() {
        if (msg.sender != admin) revert NotAdmin();
        _;
    }

    constructor(
        IERC20 settlementAsset_,
        MandateRiskManagerV1 riskManager_,
        ISpotAdapter spotAdapter_,
        address admin_,
        address recoveryRecipient_,
        address agent_
    ) {
        if (
            address(settlementAsset_).code.length == 0 || address(riskManager_).code.length == 0
                || address(spotAdapter_).code.length == 0 || admin_ == address(0)
                || recoveryRecipient_ == address(0) || agent_ == address(0) || admin_ == agent_
        ) revert InvalidAddress();
        if (
            address(riskManager_.settlementAsset()) != address(settlementAsset_)
                || address(UniswapV4SpotAdapterLike(address(spotAdapter_)).settlementAsset())
                    != address(settlementAsset_) || riskManager_.admin() != admin_
                || UniswapV4SpotAdapterLike(address(spotAdapter_)).admin() != admin_
        ) revert InvalidAddress();
        settlementAsset = settlementAsset_;
        riskManager = riskManager_;
        spotAdapter = spotAdapter_;
        admin = admin_;
        recoveryRecipient = recoveryRecipient_;
        agent = agent_;
        attestationAnchor = new AttestationAnchor(address(this));
    }

    function setAgent(address agent_) external onlyAdmin {
        if (agent_ == address(0) || agent_ == admin) revert InvalidAddress();
        agent = agent_;
        emit AgentSet(agent_);
    }

    function deposit(IERC20 token, uint256 amount) external onlyAdmin {
        if (recoveryFinalized) revert RecoveryFinalized();
        if (address(token) != address(settlementAsset) && !riskManager.isMarket(address(token))) {
            revert UnsupportedToken();
        }
        token.safeTransferFrom(admin, address(this), amount);
        emit Deposited(address(token), amount);
    }

    function recover(IERC20 token, uint256 amount) external onlyAdmin {
        if (riskManager.mode() != MandateRiskManagerV1.Mode.Halted) {
            revert RecoveryRequiresHalt();
        }
        recoveryFinalized = true;
        token.safeTransfer(recoveryRecipient, amount);
        emit Recovered(address(token), amount);
    }

    function executeSpot(SpotIntent calldata intent)
        external
        nonReentrant
        returns (uint256 amountOut)
    {
        if (msg.sender != agent) revert NotAgent();
        if (recoveryFinalized) revert RecoveryFinalized();
        (uint256 notional, uint256 price, uint256 multiplier) = riskManager.authorize(intent);

        IERC20 input = intent.side == Side.BuySpot ? settlementAsset : IERC20(intent.stockToken);
        IERC20 output = intent.side == Side.BuySpot ? IERC20(intent.stockToken) : settlementAsset;
        uint256 inputBefore = input.balanceOf(address(this));
        uint256 outputBefore = output.balanceOf(address(this));

        input.forceApprove(address(spotAdapter), intent.amountIn);
        spotAdapter.executeSpot(intent);
        input.forceApprove(address(spotAdapter), 0);

        uint256 inputAfter = input.balanceOf(address(this));
        uint256 outputAfter = output.balanceOf(address(this));
        if (inputAfter > inputBefore || outputAfter < outputBefore) revert InvalidBalanceDelta();
        uint256 actualIn = inputBefore - inputAfter;
        amountOut = outputAfter - outputBefore;
        riskManager.settle(intent.id, actualIn, amountOut);

        emit SpotExecuted(
            intent.id,
            intent.stockToken,
            intent.side,
            actualIn,
            amountOut,
            notional,
            price,
            multiplier
        );
    }

    function anchorBatch(bytes32 root, uint64 sequence, uint64 tradeCount) external {
        if (msg.sender != agent) revert NotAgent();
        attestationAnchor.anchor(root, sequence, tradeCount);
        emit BatchAnchored(root, sequence, tradeCount);
    }
}

interface UniswapV4SpotAdapterLike {
    function settlementAsset() external view returns (IERC20);
    function admin() external view returns (address);
}
