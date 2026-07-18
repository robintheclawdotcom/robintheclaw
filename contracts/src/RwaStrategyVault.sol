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
    address public immutable configAdmin;
    address public immutable treasury;
    address public agent;
    bool public recoveryFinalized;

    event AgentSet(address indexed agent);
    event AgentRevoked(address indexed caller);
    event Deposited(address indexed token, uint256 amount);
    event VaultRecoveryFinalized(address indexed treasury);
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

    error NotConfigAdmin();
    error NotTreasury();
    error NotAgent();
    error InvalidAddress();
    error InvalidAmount();
    error RecoveryRequiresHalt();
    error RecoveryNotFinalized();
    error RecoveryFinalized();
    error InvalidBalanceDelta();

    modifier onlyConfigAdmin() {
        if (msg.sender != configAdmin) revert NotConfigAdmin();
        _;
    }

    modifier onlyTreasury() {
        if (msg.sender != treasury) revert NotTreasury();
        _;
    }

    constructor(
        IERC20 settlementAsset_,
        MandateRiskManagerV1 riskManager_,
        ISpotAdapter spotAdapter_,
        address configAdmin_,
        address treasury_,
        address agent_
    ) {
        if (
            address(settlementAsset_).code.length == 0 || address(riskManager_).code.length == 0
                || address(spotAdapter_).code.length == 0 || configAdmin_ == address(0)
                || treasury_ == address(0) || configAdmin_ == treasury_
                || (agent_ != address(0) && (agent_ == configAdmin_ || agent_ == treasury_))
        ) revert InvalidAddress();
        if (
            address(riskManager_.settlementAsset()) != address(settlementAsset_)
                || address(UniswapV4SpotAdapterLike(address(spotAdapter_)).settlementAsset())
                    != address(settlementAsset_) || riskManager_.configAdmin() != configAdmin_
                || UniswapV4SpotAdapterLike(address(spotAdapter_)).configAdmin() != configAdmin_
        ) revert InvalidAddress();
        settlementAsset = settlementAsset_;
        riskManager = riskManager_;
        spotAdapter = spotAdapter_;
        configAdmin = configAdmin_;
        treasury = treasury_;
        agent = agent_;
        attestationAnchor = new AttestationAnchor(address(this));
    }

    function setAgent(address agent_) external onlyConfigAdmin {
        if (recoveryFinalized) revert RecoveryFinalized();
        if (agent_ == address(0) || agent_ == configAdmin || agent_ == treasury) {
            revert InvalidAddress();
        }
        agent = agent_;
        emit AgentSet(agent_);
    }

    function revokeAgent() external onlyTreasury {
        agent = address(0);
        emit AgentRevoked(msg.sender);
    }

    function deposit(uint256 amount) external onlyTreasury nonReentrant {
        if (recoveryFinalized) revert RecoveryFinalized();
        if (amount == 0) revert InvalidAmount();
        uint256 balanceBefore = settlementAsset.balanceOf(address(this));
        // The only caller and transfer source are the immutable treasury.
        // slither-disable-next-line arbitrary-send-erc20
        settlementAsset.safeTransferFrom(treasury, address(this), amount);
        uint256 balanceAfter = settlementAsset.balanceOf(address(this));
        if (balanceAfter < balanceBefore || balanceAfter - balanceBefore != amount) {
            revert InvalidBalanceDelta();
        }
        emit Deposited(address(settlementAsset), amount);
    }

    function finalizeRecovery() external onlyTreasury {
        if (riskManager.mode() != MandateRiskManagerV1.Mode.Halted) {
            revert RecoveryRequiresHalt();
        }
        if (recoveryFinalized) revert RecoveryFinalized();
        recoveryFinalized = true;
        agent = address(0);
        emit AgentRevoked(msg.sender);
        emit VaultRecoveryFinalized(treasury);
    }

    function recover(IERC20 token, uint256 amount) external onlyTreasury nonReentrant {
        if (riskManager.mode() != MandateRiskManagerV1.Mode.Halted) {
            revert RecoveryRequiresHalt();
        }
        if (!recoveryFinalized) revert RecoveryNotFinalized();
        if (address(token).code.length == 0) revert InvalidAddress();
        if (amount == 0) revert InvalidAmount();
        uint256 balanceBefore = token.balanceOf(address(this));
        token.safeTransfer(treasury, amount);
        uint256 balanceAfter = token.balanceOf(address(this));
        if (balanceAfter > balanceBefore || balanceBefore - balanceAfter != amount) {
            revert InvalidBalanceDelta();
        }
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
    function configAdmin() external view returns (address);
}
