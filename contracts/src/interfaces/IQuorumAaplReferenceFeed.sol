// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IChainlinkFeed } from "./IChainlinkFeed.sol";

interface IQuorumAaplReferenceFeed is IChainlinkFeed {
    function publisherCount() external pure returns (uint8);
    function quorum() external pure returns (uint8);
    function maxReportAge() external pure returns (uint64);
    function maxSourceAge() external pure returns (uint64);
    function publisher1() external view returns (address);
    function publisher2() external view returns (address);
    function publisher3() external view returns (address);
}
