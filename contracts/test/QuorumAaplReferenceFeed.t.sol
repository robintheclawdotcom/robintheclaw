// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { QuorumAaplReferenceFeed } from "../src/QuorumAaplReferenceFeed.sol";

contract QuorumAaplReferenceFeedTest is Test {
    address private publisher1 = makeAddr("aapl-publisher-1");
    address private publisher2 = makeAddr("aapl-publisher-2");
    address private publisher3 = makeAddr("aapl-publisher-3");
    QuorumAaplReferenceFeed private feed;

    function setUp() public {
        vm.warp(10_000);
        feed = new QuorumAaplReferenceFeed(publisher1, publisher2, publisher3);
    }

    function testPreservesExactSourceRoundAndTimestampWithQuorum() public {
        _report(publisher1, 1, 52, 21_345_678_901, 9_990, 53);
        _assertDown();

        _report(publisher2, 4, 52, 21_345_678_901, 9_990, 53);
        (uint80 roundId, int256 answer, uint256 startedAt, uint256 updatedAt, uint80 answered) =
            feed.latestRoundData();
        assertEq(roundId, 52);
        assertEq(answer, 21_345_678_901);
        assertEq(startedAt, 9_990);
        assertEq(updatedAt, 9_990);
        assertEq(answered, 53);
        assertEq(feed.decimals(), 8);
    }

    function testReportsDownOnAnyFreshDisagreement() public {
        _report(publisher1, 1, 52, 21_345_678_901, 9_990, 52);
        _report(publisher2, 1, 52, 21_345_678_901, 9_990, 52);
        _report(publisher3, 1, 52, 21_345_678_902, 9_990, 52);
        _assertDown();
    }

    function testIgnoresOneExpiredRelayButRequiresTwoFreshReports() public {
        _report(publisher1, 1, 52, 21_345_678_901, 9_990, 52);
        vm.warp(block.timestamp + 40);
        _report(publisher2, 1, 53, 21_345_678_901, 10_030, 53);
        _report(publisher3, 1, 53, 21_345_678_901, 10_030, 53);
        vm.warp(block.timestamp + 21);

        (uint80 roundId, int256 answer,,,) = feed.latestRoundData();
        assertEq(roundId, 53);
        assertEq(answer, 21_345_678_901);

        vm.warp(block.timestamp + 40);
        _assertDown();
    }

    function testRejectsStaleSourceUnauthorizedPublisherAndReplay() public {
        vm.expectRevert(QuorumAaplReferenceFeed.NotPublisher.selector);
        feed.report(1, 52, 21_345_678_901, 9_990, 52);

        vm.warp(100_000);
        vm.prank(publisher1);
        vm.expectRevert(QuorumAaplReferenceFeed.InvalidReport.selector);
        feed.report(1, 52, 21_345_678_901, 9_999, 52);

        _report(publisher1, 2, 52, 21_345_678_901, 99_990, 52);
        vm.prank(publisher1);
        vm.expectRevert(QuorumAaplReferenceFeed.InvalidReport.selector);
        feed.report(2, 52, 21_345_678_901, 99_990, 52);
    }

    function testRejectsSourceRoundRegressionAndMutation() public {
        _report(publisher1, 1, 52, 21_345_678_901, 9_990, 52);

        vm.prank(publisher1);
        vm.expectRevert(QuorumAaplReferenceFeed.InvalidReport.selector);
        feed.report(2, 51, 21_345_678_901, 9_991, 51);

        vm.prank(publisher1);
        vm.expectRevert(QuorumAaplReferenceFeed.InvalidReport.selector);
        feed.report(2, 52, 21_345_678_902, 9_990, 52);

        vm.prank(publisher1);
        vm.expectRevert(QuorumAaplReferenceFeed.InvalidReport.selector);
        feed.report(2, 53, 21_345_678_901, 9_991, 52);
    }

    function testFuzzExactTwoPublisherConsensus(uint80 roundId, int192 answer, uint64 age) public {
        vm.warp(100_000);
        roundId = uint80(bound(roundId, 1, type(uint80).max));
        answer = int192(bound(int256(answer), 1, int256(type(int192).max)));
        age = uint64(bound(age, 0, feed.maxSourceAge()));
        uint64 updatedAt = uint64(block.timestamp) - age;
        _report(publisher1, 1, roundId, answer, updatedAt, roundId);
        _report(publisher2, 1, roundId, answer, updatedAt, roundId);

        (uint80 actualRound, int256 actualAnswer,, uint256 actualUpdatedAt, uint80 answered) =
            feed.latestRoundData();
        assertEq(actualRound, roundId);
        assertEq(actualAnswer, answer);
        assertEq(actualUpdatedAt, updatedAt);
        assertEq(answered, roundId);
    }

    function testRejectsDuplicatePublishers() public {
        vm.expectRevert(QuorumAaplReferenceFeed.InvalidPublishers.selector);
        new QuorumAaplReferenceFeed(publisher1, publisher1, publisher3);
    }

    function _report(
        address publisher,
        uint64 sequence,
        uint80 sourceRoundId,
        int192 answer,
        uint64 sourceUpdatedAt,
        uint80 sourceAnsweredInRound
    ) private {
        vm.prank(publisher);
        feed.report(sequence, sourceRoundId, answer, sourceUpdatedAt, sourceAnsweredInRound);
    }

    function _assertDown() private view {
        (uint80 roundId, int256 answer, uint256 startedAt, uint256 updatedAt, uint80 answered) =
            feed.latestRoundData();
        assertEq(roundId, 0);
        assertEq(answer, 0);
        assertEq(startedAt, block.timestamp);
        assertEq(updatedAt, block.timestamp);
        assertEq(answered, 0);
    }
}
