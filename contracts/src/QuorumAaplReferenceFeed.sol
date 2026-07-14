// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IQuorumAaplReferenceFeed } from "./interfaces/IQuorumAaplReferenceFeed.sol";

contract QuorumAaplReferenceFeed is IQuorumAaplReferenceFeed {
    struct Report {
        uint64 sequence;
        uint64 relayedAt;
        uint64 sourceUpdatedAt;
        uint80 sourceRoundId;
        uint80 sourceAnsweredInRound;
        int192 answer;
    }

    uint8 public constant override publisherCount = 3;
    uint8 public constant override quorum = 2;
    uint64 public constant override maxReportAge = 60 seconds;
    uint64 public constant override maxSourceAge = 25 hours;

    address public immutable override publisher1;
    address public immutable override publisher2;
    address public immutable override publisher3;

    mapping(address => Report) public reports;

    event PriceReported(
        address indexed publisher,
        uint64 indexed sequence,
        uint80 indexed sourceRoundId,
        int192 answer,
        uint64 sourceUpdatedAt,
        uint80 sourceAnsweredInRound,
        uint64 relayedAt
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

    function report(
        uint64 sequence,
        uint80 sourceRoundId,
        int192 answer,
        uint64 sourceUpdatedAt,
        uint80 sourceAnsweredInRound
    ) external {
        if (msg.sender != publisher1 && msg.sender != publisher2 && msg.sender != publisher3) {
            revert NotPublisher();
        }
        Report storage current = reports[msg.sender];
        if (
            sequence == 0 || sequence <= current.sequence || sourceRoundId == 0 || answer <= 0
                || sourceUpdatedAt == 0 || sourceUpdatedAt > block.timestamp
                || block.timestamp - sourceUpdatedAt > maxSourceAge
                || sourceAnsweredInRound < sourceRoundId || sourceRoundId < current.sourceRoundId
                || sourceUpdatedAt < current.sourceUpdatedAt
                || (sourceRoundId == current.sourceRoundId
                    && current.sourceRoundId != 0
                    && (answer != current.answer
                        || sourceUpdatedAt != current.sourceUpdatedAt
                        || sourceAnsweredInRound != current.sourceAnsweredInRound))
        ) revert InvalidReport();

        uint64 relayedAt = uint64(block.timestamp);
        reports[msg.sender] = Report({
            sequence: sequence,
            relayedAt: relayedAt,
            sourceUpdatedAt: sourceUpdatedAt,
            sourceRoundId: sourceRoundId,
            sourceAnsweredInRound: sourceAnsweredInRound,
            answer: answer
        });
        emit PriceReported(
            msg.sender,
            sequence,
            sourceRoundId,
            answer,
            sourceUpdatedAt,
            sourceAnsweredInRound,
            relayedAt
        );
    }

    function decimals() external pure override returns (uint8) {
        return 8;
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
        Report memory expected;
        bool hasExpected;
        uint8 freshReports;

        for (uint256 i; i < publishers.length; ++i) {
            Report memory current = reports[publishers[i]];
            if (
                current.relayedAt == 0 || block.timestamp - current.relayedAt > maxReportAge
                    || current.sourceUpdatedAt == 0
                    || block.timestamp - current.sourceUpdatedAt > maxSourceAge
            ) continue;

            ++freshReports;
            if (!hasExpected) {
                expected = current;
                hasExpected = true;
            } else if (
                current.sourceRoundId != expected.sourceRoundId || current.answer != expected.answer
                    || current.sourceUpdatedAt != expected.sourceUpdatedAt
                    || current.sourceAnsweredInRound != expected.sourceAnsweredInRound
            ) {
                return _down();
            }
        }

        if (freshReports < quorum) return _down();
        roundId = expected.sourceRoundId;
        answer = expected.answer;
        startedAt = expected.sourceUpdatedAt;
        updatedAt = expected.sourceUpdatedAt;
        answeredInRound = expected.sourceAnsweredInRound;
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
        return (0, 0, block.timestamp, block.timestamp, 0);
    }
}
