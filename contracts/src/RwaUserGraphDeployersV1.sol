// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Create2 } from "@openzeppelin/contracts/utils/Create2.sol";
import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { IERC20Metadata } from "@openzeppelin/contracts/token/ERC20/extensions/IERC20Metadata.sol";
import { MandateRiskManagerV1 } from "./MandateRiskManagerV1.sol";
import { RwaUserStrategyVaultV1 } from "./RwaUserStrategyVaultV1.sol";
import { UniswapV4SpotAdapter } from "./UniswapV4SpotAdapter.sol";
import { IChainlinkFeed } from "./interfaces/IChainlinkFeed.sol";
import { IMainnetExecutionRegistry } from "./interfaces/IMainnetExecutionRegistry.sol";
import { ISpotAdapter } from "./interfaces/ISpotAdapter.sol";
import { IPermit2AllowanceTransfer, IUniversalRouter } from "./interfaces/IUniswapV4.sol";

abstract contract UserComponentDeployerV1 {
    address public immutable factory;

    error NotFactory();

    constructor() {
        factory = msg.sender;
    }

    modifier onlyFactory() {
        if (msg.sender != factory) revert NotFactory();
        _;
    }
}

contract UserRiskManagerDeployerV1 is UserComponentDeployerV1 {
    function deploy(
        bytes32 salt,
        IERC20Metadata settlementAsset,
        IChainlinkFeed sequencerFeed,
        address registry,
        address guardian,
        address owner,
        uint256 grossNotionalLimit,
        uint256 turnoverLimit,
        uint64 turnoverWindow,
        uint64 maxDeadlineDelay,
        uint64 sequencerGracePeriod
    ) external onlyFactory returns (MandateRiskManagerV1 risk) {
        risk = new MandateRiskManagerV1{ salt: salt }(
            settlementAsset,
            sequencerFeed,
            registry,
            guardian,
            owner,
            factory,
            grossNotionalLimit,
            turnoverLimit,
            turnoverWindow,
            maxDeadlineDelay,
            sequencerGracePeriod,
            1
        );
    }

    function predict(
        bytes32 salt,
        IERC20Metadata settlementAsset,
        IChainlinkFeed sequencerFeed,
        address registry,
        address guardian,
        address owner,
        uint256 grossNotionalLimit,
        uint256 turnoverLimit,
        uint64 turnoverWindow,
        uint64 maxDeadlineDelay,
        uint64 sequencerGracePeriod
    ) external view returns (address) {
        bytes memory code = abi.encodePacked(
            type(MandateRiskManagerV1).creationCode,
            abi.encode(
                settlementAsset,
                sequencerFeed,
                registry,
                guardian,
                owner,
                factory,
                grossNotionalLimit,
                turnoverLimit,
                turnoverWindow,
                maxDeadlineDelay,
                sequencerGracePeriod,
                uint8(1)
            )
        );
        return Create2.computeAddress(salt, keccak256(code));
    }
}

contract UserSpotAdapterDeployerV1 is UserComponentDeployerV1 {
    function deploy(
        bytes32 salt,
        IERC20 settlementAsset,
        IUniversalRouter router,
        IPermit2AllowanceTransfer permit2,
        address registry,
        bytes32 routerCodeHash,
        bytes32 permit2CodeHash
    ) external onlyFactory returns (UniswapV4SpotAdapter adapter) {
        adapter = new UniswapV4SpotAdapter{ salt: salt }(
            settlementAsset, router, permit2, registry, factory, routerCodeHash, permit2CodeHash
        );
    }

    function predict(
        bytes32 salt,
        IERC20 settlementAsset,
        IUniversalRouter router,
        IPermit2AllowanceTransfer permit2,
        address registry,
        bytes32 routerCodeHash,
        bytes32 permit2CodeHash
    ) external view returns (address) {
        bytes memory code = abi.encodePacked(
            type(UniswapV4SpotAdapter).creationCode,
            abi.encode(
                settlementAsset, router, permit2, registry, factory, routerCodeHash, permit2CodeHash
            )
        );
        return Create2.computeAddress(salt, keccak256(code));
    }
}

contract UserVaultDeployerV1 is UserComponentDeployerV1 {
    function deploy(
        bytes32 salt,
        IERC20 settlementAsset,
        MandateRiskManagerV1 riskManager,
        ISpotAdapter spotAdapter,
        IMainnetExecutionRegistry registry,
        address owner
    ) external onlyFactory returns (RwaUserStrategyVaultV1 vault) {
        vault = new RwaUserStrategyVaultV1{ salt: salt }(
            settlementAsset, riskManager, spotAdapter, registry, owner
        );
    }

    function predict(
        bytes32 salt,
        IERC20 settlementAsset,
        MandateRiskManagerV1 riskManager,
        ISpotAdapter spotAdapter,
        IMainnetExecutionRegistry registry,
        address owner
    ) external view returns (address) {
        bytes memory code = abi.encodePacked(
            type(RwaUserStrategyVaultV1).creationCode,
            abi.encode(settlementAsset, riskManager, spotAdapter, registry, owner)
        );
        return Create2.computeAddress(salt, keccak256(code));
    }
}
