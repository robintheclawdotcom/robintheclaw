// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { SafeERC20 } from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import { ReentrancyGuard } from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import { AttestationAnchor } from "./AttestationAnchor.sol";
import { MandateRiskManagerV1 } from "./MandateRiskManagerV1.sol";
import { IMainnetExecutionRegistry } from "./interfaces/IMainnetExecutionRegistry.sol";
import { ISpotAdapter } from "./interfaces/ISpotAdapter.sol";
import { ISpotExecution } from "./interfaces/ISpotExecution.sol";

contract RwaUserStrategyVaultV1 is ISpotExecution, ReentrancyGuard {
    using SafeERC20 for IERC20;

    uint256 public constant CHAIN_ID = 4663;

    IERC20 public immutable settlementAsset;
    bytes32 public immutable settlementAssetCodeHash;
    MandateRiskManagerV1 public immutable riskManager;
    ISpotAdapter public immutable spotAdapter;
    IMainnetExecutionRegistry public immutable registry;
    AttestationAnchor public immutable attestationAnchor;
    address public immutable owner;
    address public agent;
    bool public agentEnabled;
    bool public initialAgentAuthorized;
    bool public recoveryFinalized;

    event AgentSet(address indexed agent);
    event AgentEnabled(address indexed owner, address indexed agent);
    event AgentRevoked(address indexed caller);
    event EmergencyHalted(address indexed owner);
    event Deposited(address indexed token, uint256 amount);
    event SettlementWithdrawn(uint256 amount);
    event VaultRecoveryFinalized(address indexed owner);
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

    error UnsupportedChain(uint256 chainId);
    error NotRegistry();
    error NotOwner();
    error NotAgent();
    error AgentNotEnabled();
    error InitialAgentAlreadyAuthorized();
    error InvalidAddress();
    error InvalidAmount();
    error RecoveryRequiresHalt();
    error RecoveryNotFinalized();
    error RecoveryFinalized();
    error InvalidBalanceDelta();
    error VaultNotFlat();
    error AgentStillAuthorized();
    error GlobalHalt();
    error GlobalReduceOnly();
    error UnregisteredVault();
    error EpisodeAlreadyActive();
    error FullExitRequired(uint256 expected, uint256 actual);

    modifier onlyRegistry() {
        if (msg.sender != address(registry)) revert NotRegistry();
        _;
    }

    modifier onlyOwner() {
        if (msg.sender != owner) revert NotOwner();
        _;
    }

    constructor(
        IERC20 settlementAsset_,
        MandateRiskManagerV1 riskManager_,
        ISpotAdapter spotAdapter_,
        IMainnetExecutionRegistry registry_,
        address owner_
    ) {
        if (block.chainid != CHAIN_ID) {
            revert UnsupportedChain(block.chainid);
        }
        if (
            address(settlementAsset_).code.length == 0 || address(riskManager_).code.length == 0
                || address(spotAdapter_).code.length == 0 || address(registry_).code.length == 0
                || owner_ == address(0) || owner_ == address(registry_)
                || owner_ == registry_.guardian()
        ) revert InvalidAddress();
        if (
            address(riskManager_.settlementAsset()) != address(settlementAsset_)
                || address(UserSpotAdapterLike(address(spotAdapter_)).settlementAsset())
                    != address(settlementAsset_) || riskManager_.configAdmin() != address(registry_)
                || UserSpotAdapterLike(address(spotAdapter_)).configAdmin() != address(registry_)
                || riskManager_.treasury() != owner_
        ) revert InvalidAddress();

        settlementAsset = settlementAsset_;
        settlementAssetCodeHash = address(settlementAsset_).codehash;
        riskManager = riskManager_;
        spotAdapter = spotAdapter_;
        registry = registry_;
        owner = owner_;
        attestationAnchor = new AttestationAnchor(address(this));
    }

    function setAgent(address agent_) external onlyRegistry {
        if (recoveryFinalized) revert RecoveryFinalized();
        _validateAgent(agent_);
        agent = agent_;
        agentEnabled = false;
        initialAgentAuthorized = true;
        emit AgentSet(agent_);
    }

    function authorizeInitialAgent(address agent_) external onlyOwner {
        if (recoveryFinalized) revert RecoveryFinalized();
        if (initialAgentAuthorized) revert InitialAgentAlreadyAuthorized();
        _validateAgent(agent_);
        agent = agent_;
        agentEnabled = true;
        initialAgentAuthorized = true;
        emit AgentSet(agent_);
        emit AgentEnabled(owner, agent_);
    }

    function enableAgent() external onlyOwner {
        if (recoveryFinalized) revert RecoveryFinalized();
        address agent_ = agent;
        if (agent_ == address(0)) revert InvalidAddress();
        agentEnabled = true;
        emit AgentEnabled(owner, agent_);
    }

    function revokeAgent() external onlyOwner {
        agent = address(0);
        agentEnabled = false;
        emit AgentRevoked(msg.sender);
    }

    function emergencyHalt() external onlyOwner {
        riskManager.haltFromExecutor();
        agent = address(0);
        agentEnabled = false;
        emit AgentRevoked(msg.sender);
        emit EmergencyHalted(owner);
    }

    function deposit(uint256 amount) external onlyOwner nonReentrant {
        if (recoveryFinalized) revert RecoveryFinalized();
        if (amount == 0) revert InvalidAmount();
        _checkSettlementCode();
        uint256 balanceBefore = settlementAsset.balanceOf(address(this));
        settlementAsset.safeTransferFrom(owner, address(this), amount);
        uint256 balanceAfter = settlementAsset.balanceOf(address(this));
        if (balanceAfter < balanceBefore || balanceAfter - balanceBefore != amount) {
            revert InvalidBalanceDelta();
        }
        emit Deposited(address(settlementAsset), amount);
    }

    function isFlat() public view returns (bool) {
        return riskManager.activeMarketCount() == 0 && riskManager.pendingIntent() == bytes32(0);
    }

    function withdrawSettlement(uint256 amount) external onlyOwner nonReentrant {
        if (recoveryFinalized) revert RecoveryFinalized();
        if (riskManager.mode() != MandateRiskManagerV1.Mode.Halted) {
            revert RecoveryRequiresHalt();
        }
        if (!isFlat()) revert VaultNotFlat();
        if (agent != address(0)) revert AgentStillAuthorized();
        if (amount == 0) revert InvalidAmount();
        _checkSettlementCode();

        uint256 balanceBefore = settlementAsset.balanceOf(address(this));
        settlementAsset.safeTransfer(owner, amount);
        uint256 balanceAfter = settlementAsset.balanceOf(address(this));
        if (balanceAfter > balanceBefore || balanceBefore - balanceAfter != amount) {
            revert InvalidBalanceDelta();
        }
        emit SettlementWithdrawn(amount);
    }

    function finalizeRecovery() external onlyOwner {
        if (riskManager.mode() != MandateRiskManagerV1.Mode.Halted) {
            revert RecoveryRequiresHalt();
        }
        if (recoveryFinalized) revert RecoveryFinalized();
        recoveryFinalized = true;
        agent = address(0);
        agentEnabled = false;
        emit AgentRevoked(msg.sender);
        emit VaultRecoveryFinalized(owner);
    }

    function recover(IERC20 token, uint256 amount) external onlyOwner nonReentrant {
        if (riskManager.mode() != MandateRiskManagerV1.Mode.Halted) {
            revert RecoveryRequiresHalt();
        }
        if (!recoveryFinalized) revert RecoveryNotFinalized();
        if (address(token).code.length == 0) revert InvalidAddress();
        if (amount == 0) revert InvalidAmount();
        uint256 balanceBefore = token.balanceOf(address(this));
        token.safeTransfer(owner, amount);
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
        if (!agentEnabled) revert AgentNotEnabled();
        if (recoveryFinalized) revert RecoveryFinalized();
        _checkSettlementCode();
        _checkGlobalMode(intent.side);
        _checkEpisodeShape(intent);
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
        if (!agentEnabled) revert AgentNotEnabled();
        attestationAnchor.anchor(root, sequence, tradeCount);
        emit BatchAnchored(root, sequence, tradeCount);
    }

    function _checkGlobalMode(Side side) private view {
        if (!registry.isRegisteredVault(address(this))) revert UnregisteredVault();
        IMainnetExecutionRegistry.Mode mode = registry.globalMode();
        if (mode == IMainnetExecutionRegistry.Mode.Halted) revert GlobalHalt();
        if (side == Side.BuySpot && mode != IMainnetExecutionRegistry.Mode.Active) {
            revert GlobalReduceOnly();
        }
    }

    function _checkSettlementCode() private view {
        bytes32 actual = address(settlementAsset).codehash;
        if (actual != settlementAssetCodeHash) {
            revert MandateRiskManagerV1.ExternalCodeChanged(
                address(settlementAsset), settlementAssetCodeHash, actual
            );
        }
    }

    function _checkEpisodeShape(SpotIntent calldata intent) private view {
        uint256 inventory = riskManager.inventory(intent.stockToken);
        if (intent.side == Side.BuySpot) {
            if (inventory != 0) revert EpisodeAlreadyActive();
        } else if (intent.amountIn != inventory) {
            revert FullExitRequired(inventory, intent.amountIn);
        }
    }

    function _validateAgent(address agent_) private view {
        if (
            agent_ == address(0) || agent_ == owner || agent_ == address(registry)
                || agent_ == registry.configAdmin() || agent_ == registry.guardian()
        ) revert InvalidAddress();
    }
}

interface UserSpotAdapterLike {
    function settlementAsset() external view returns (IERC20);
    function configAdmin() external view returns (address);
}
