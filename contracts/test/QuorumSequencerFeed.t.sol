// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { QuorumSequencerFeed } from "../src/QuorumSequencerFeed.sol";

contract QuorumSequencerFeedTest is Test {
    address private publisher1 = makeAddr("publisher-1");
    address private publisher2 = makeAddr("publisher-2");
    address private publisher3 = makeAddr("publisher-3");
    QuorumSequencerFeed private feed;

    function setUp() public {
        vm.warp(10_000);
        feed = new QuorumSequencerFeed(publisher1, publisher2, publisher3);
    }

    function testRequiresTwoFreshAgreeingReports() public {
        _report(publisher1, 1, true, 8_000);
        _assertDown();

        _report(publisher2, 3, true, 9_000);
        (uint80 roundId, int256 answer, uint256 startedAt, uint256 updatedAt, uint80 answered) =
            feed.latestRoundData();
        assertEq(roundId, 3);
        assertEq(answer, 0);
        assertEq(startedAt, 9_000);
        assertEq(updatedAt, block.timestamp);
        assertEq(answered, roundId);
    }

    function testReportsDownOnFreshDisagreement() public {
        _report(publisher1, 1, true, 8_000);
        _report(publisher2, 1, true, 8_000);
        _report(publisher3, 1, false, 9_500);
        _assertDown();
    }

    function testIgnoresOneStalePublisherButFailsWhenQuorumExpires() public {
        _report(publisher1, 1, true, 8_000);
        _report(publisher2, 1, true, 8_000);
        vm.warp(block.timestamp + 40);
        _report(publisher2, 2, true, 8_000);
        _report(publisher3, 1, true, 8_000);
        vm.warp(block.timestamp + 30);

        (, int256 answer,,,) = feed.latestRoundData();
        assertEq(answer, 0);

        vm.warp(block.timestamp + 31);
        _assertDown();
    }

    function testRejectsUnauthorizedAndReplayReports() public {
        vm.expectRevert(QuorumSequencerFeed.NotPublisher.selector);
        feed.report(1, true, 1);

        _report(publisher1, 2, true, 1);
        vm.prank(publisher1);
        vm.expectRevert(QuorumSequencerFeed.InvalidReport.selector);
        feed.report(2, true, 1);
    }

    function testConstructorRejectsDuplicatePublishers() public {
        vm.expectRevert(QuorumSequencerFeed.InvalidPublishers.selector);
        new QuorumSequencerFeed(publisher1, publisher1, publisher3);
    }

    function _report(address publisher, uint64 sequence, bool healthy, uint64 startedAt) private {
        vm.prank(publisher);
        feed.report(sequence, healthy, startedAt);
    }

    function _assertDown() private view {
        (uint80 roundId, int256 answer, uint256 startedAt, uint256 updatedAt, uint80 answered) =
            feed.latestRoundData();
        assertEq(roundId, 0);
        assertEq(answer, 1);
        assertEq(startedAt, block.timestamp);
        assertEq(updatedAt, block.timestamp);
        assertEq(answered, 0);
    }
}
