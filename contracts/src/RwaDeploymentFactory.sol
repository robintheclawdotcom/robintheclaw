// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { IERC20Metadata } from "@openzeppelin/contracts/token/ERC20/extensions/IERC20Metadata.sol";
import { IChainlinkFeed } from "./interfaces/IChainlinkFeed.sol";
import { IPermit2AllowanceTransfer, IUniversalRouter } from "./interfaces/IUniswapV4.sol";
import { MandateRiskManagerV1 } from "./MandateRiskManagerV1.sol";
import { RwaStrategyVault } from "./RwaStrategyVault.sol";
import { UniswapV4SpotAdapter } from "./UniswapV4SpotAdapter.sol";

contract RwaDeploymentFactory {
    struct Config {
        IERC20Metadata settlementAsset;
        IChainlinkFeed sequencerFeed;
        IUniversalRouter router;
        IPermit2AllowanceTransfer permit2;
        address admin;
        address recoveryRecipient;
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

    MandateRiskManagerV1 public immutable riskManager;
    UniswapV4SpotAdapter public immutable spotAdapter;
    RwaStrategyVault public immutable vault;

    error InvalidRoles();

    constructor(Config memory config) {
        if (
            config.admin == address(0) || config.recoveryRecipient == address(0)
                || config.guardian == address(0) || config.agent == address(0)
                || config.admin == config.guardian || config.admin == config.agent
                || config.guardian == config.agent || config.recoveryRecipient == config.admin
                || config.recoveryRecipient == config.agent
        ) revert InvalidRoles();
        MandateRiskManagerV1 risk = new MandateRiskManagerV1(
            config.settlementAsset,
            config.sequencerFeed,
            config.admin,
            config.guardian,
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
            config.admin,
            address(this),
            config.routerCodeHash,
            config.permit2CodeHash
        );
        RwaStrategyVault vault_ = new RwaStrategyVault(
            IERC20(address(config.settlementAsset)),
            risk,
            adapter,
            config.admin,
            config.recoveryRecipient,
            config.agent
        );
        risk.bindExecutor(address(vault_));
        adapter.bindVault(address(vault_));

        riskManager = risk;
        spotAdapter = adapter;
        vault = vault_;
    }
}
