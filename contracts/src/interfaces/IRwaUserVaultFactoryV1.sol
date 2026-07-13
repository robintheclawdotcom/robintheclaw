// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { PoolKey } from "./IUniswapV4.sol";

interface IRwaUserVaultFactoryV1 {
    struct Policy {
        address settlementAsset;
        address stockToken;
        address marketFeed;
        address sequencerFeed;
        address router;
        address permit2;
        PoolKey poolKey;
        bytes32 settlementAssetCodeHash;
        bytes32 stockTokenCodeHash;
        bytes32 marketFeedCodeHash;
        bytes32 sequencerFeedCodeHash;
        bytes32 routerCodeHash;
        bytes32 permit2CodeHash;
        uint128 maxInventory;
        uint64 marketVersion;
        uint64 heartbeat;
        uint64 maxDeadlineDelay;
        uint64 sequencerGracePeriod;
        uint64 policyVersion;
        uint16 maxSlippageBps;
        uint256 maxSpotNotional;
        uint256 maxPairGross;
        uint256 turnoverLimit;
        uint64 turnoverWindow;
    }

    struct Graph {
        address riskManager;
        address spotAdapter;
        address vault;
    }

    function registry() external view returns (address);
    function policy() external view returns (Policy memory);
    function policyDigest() external view returns (bytes32);
    function graphForOwner(address owner) external view returns (Graph memory);
}
