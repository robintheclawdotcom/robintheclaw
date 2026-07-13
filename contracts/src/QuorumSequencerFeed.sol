// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IQuorumSequencerFeed } from "./interfaces/IQuorumSequencerFeed.sol";

contract QuorumSequencerFeed is IQuorumSequencerFeed {
    struct Report {
        uint64 sequence;
        uint64 startedAt;
        uint64 updatedAt;
        bool healthy;
    }

    uint8 public constant override publisherCount = 3;
    uint8 public constant override quorum = 2;
    uint64 public constant override maxAge = 60 seconds;

    address public immutable publisher1;
    address public immutable publisher2;
    address public immutable publisher3;

    mapping(address => Report) public reports;

    event HealthReported(
        address indexed publisher,
        uint64 indexed sequence,
        bool healthy,
        uint64 startedAt,
        uint64 updatedAt
    );

    error NotPublisher();
    error InvalidPublishers();
    error InvalidReport();

    constructor(address publisher1_, address publisher2_, address publisher3_) {
        if (
            publisher1_ == address(0) || publisher2_ == address(0) || publisher3_ == address(0)
                || publisher1_ == publisher2_ || publisher1_ == publisher3_
                || publisher2_ == publisher3_
        ) revert InvalidPublishers();
        publisher1 = publisher1_;
        publisher2 = publisher2_;
        publisher3 = publisher3_;
    }

    function report(uint64 sequence, bool healthy, uint64 startedAt) external {
        if (msg.sender != publisher1 && msg.sender != publisher2 && msg.sender != publisher3) {
            revert NotPublisher();
        }
        Report storage current = reports[msg.sender];
        if (
            sequence == 0 || sequence <= current.sequence || startedAt == 0
                || startedAt > block.timestamp
        ) revert InvalidReport();

        uint64 updatedAt = uint64(block.timestamp);
        reports[msg.sender] = Report({
            sequence: sequence, startedAt: startedAt, updatedAt: updatedAt, healthy: healthy
        });
        emit HealthReported(msg.sender, sequence, healthy, startedAt, updatedAt);
    }

    function decimals() external pure override returns (uint8) {
        return 0;
    }

    function latestRoundData()
        external
        view
        override
        returns (
            uint80 roundId,
            int256 answer,
            uint256 startedAt,
            uint256 updatedAt,
            uint80 answeredInRound
        )
    {
        address[3] memory publishers = [publisher1, publisher2, publisher3];
        bool expectedHealth;
        bool hasExpectedHealth;
        uint256 oldestUpdate = type(uint256).max;
        uint8 freshReports;

        for (uint256 i; i < publishers.length; ++i) {
            Report memory current = reports[publishers[i]];
            if (current.updatedAt == 0 || block.timestamp - current.updatedAt > maxAge) continue;

            ++freshReports;
            if (!hasExpectedHealth) {
                expectedHealth = current.healthy;
                hasExpectedHealth = true;
            } else if (current.healthy != expectedHealth) {
                return _down();
            }

            if (current.startedAt > startedAt) startedAt = current.startedAt;
            if (current.updatedAt < oldestUpdate) oldestUpdate = current.updatedAt;
            if (current.sequence > roundId) roundId = uint80(current.sequence);
        }

        if (freshReports < quorum) return _down();

        answer = expectedHealth ? int256(0) : int256(1);
        updatedAt = oldestUpdate;
        answeredInRound = roundId;
    }

    function _down()
        private
        view
        returns (
            uint80 roundId,
            int256 answer,
            uint256 startedAt,
            uint256 updatedAt,
            uint80 answeredInRound
        )
    {
        return (0, 1, block.timestamp, block.timestamp, 0);
    }
}
