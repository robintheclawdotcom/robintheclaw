// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { IERC20Metadata } from "@openzeppelin/contracts/token/ERC20/extensions/IERC20Metadata.sol";
import { IPermit2AllowanceTransfer, IUniversalRouter } from "./interfaces/IUniswapV4.sol";
import { MandateRiskManagerV1 } from "./MandateRiskManagerV1.sol";
import { RwaStrategyVault } from "./RwaStrategyVault.sol";
import { SequencerGate } from "./SequencerGate.sol";
import { UniswapV4SpotAdapter } from "./UniswapV4SpotAdapter.sol";

contract RwaDeploymentFactory {
    struct Config {
        IERC20Metadata settlementAsset;
        IUniversalRouter router;
        IPermit2AllowanceTransfer permit2;
        address configAdmin;
        address treasury;
        address guardian;
        address agent;
        bytes32 routerCodeHash;
        bytes32 permit2CodeHash;
        uint256 grossNotionalLimit;
        uint256 turnoverLimit;
        uint64 turnoverWindow;
        uint64 maxDeadlineDelay;
        uint64 sequencerGracePeriod;
        uint8 maxActiveMarkets;
    }

    address public immutable configAdmin;
    address public immutable treasury;
    address public immutable guardian;
    address public immutable initialAgent;
    SequencerGate public immutable sequencerGate;
    MandateRiskManagerV1 public immutable riskManager;
    UniswapV4SpotAdapter public immutable spotAdapter;
    RwaStrategyVault public immutable vault;

    error InvalidRoles();

    constructor(Config memory config) {
        if (
            config.configAdmin.code.length == 0 || config.treasury.code.length == 0
                || config.guardian == address(0) || config.configAdmin == config.treasury
                || config.configAdmin == config.guardian || config.treasury == config.guardian
                || (config.agent != address(0)
                    && (config.agent == config.configAdmin
                        || config.agent == config.treasury
                        || config.agent == config.guardian))
        ) revert InvalidRoles();

        SequencerGate gate = new SequencerGate(config.configAdmin);
        MandateRiskManagerV1 risk = new MandateRiskManagerV1(
            config.settlementAsset,
            gate,
            config.configAdmin,
            config.guardian,
            config.treasury,
            address(this),
            config.grossNotionalLimit,
            config.turnoverLimit,
            config.turnoverWindow,
            config.maxDeadlineDelay,
            config.sequencerGracePeriod,
            config.maxActiveMarkets
        );
        UniswapV4SpotAdapter adapter = new UniswapV4SpotAdapter(
            IERC20(address(config.settlementAsset)),
            config.router,
            config.permit2,
            config.configAdmin,
            address(this),
            config.routerCodeHash,
            config.permit2CodeHash
        );
        RwaStrategyVault vault_ = new RwaStrategyVault(
            IERC20(address(config.settlementAsset)),
            risk,
            adapter,
            config.configAdmin,
            config.treasury,
            config.agent
        );
        risk.bindExecutor(address(vault_));
        adapter.bindVault(address(vault_));

        configAdmin = config.configAdmin;
        treasury = config.treasury;
        guardian = config.guardian;
        initialAgent = config.agent;
        sequencerGate = gate;
        riskManager = risk;
        spotAdapter = adapter;
        vault = vault_;
    }
}
