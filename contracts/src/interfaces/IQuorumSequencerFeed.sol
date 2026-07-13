// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IChainlinkFeed } from "./IChainlinkFeed.sol";

interface IQuorumSequencerFeed is IChainlinkFeed {
    function publisherCount() external pure returns (uint8);
    function quorum() external pure returns (uint8);
    function maxAge() external pure returns (uint64);
}
