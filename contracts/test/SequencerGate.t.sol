// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Test } from "forge-std/Test.sol";
import { IChainlinkFeed } from "../src/interfaces/IChainlinkFeed.sol";
import { SequencerGate } from "../src/SequencerGate.sol";

contract GateAdmin { }

contract MockSequencerFeed is IChainlinkFeed {
    uint8 public feedDecimals;
    uint80 public roundId = 7;
    int256 public answer;
    uint256 public startedAt;
    uint256 public updatedAt;
    uint80 public answeredInRound = 7;

    constructor() {
        startedAt = block.timestamp - 1 hours;
        updatedAt = block.timestamp;
    }

    function setDecimals(uint8 decimals_) external {
        feedDecimals = decimals_;
    }

    function setRound(int256 answer_, uint256 startedAt_, uint256 updatedAt_) external {
        answer = answer_;
        startedAt = startedAt_;
        updatedAt = updatedAt_;
    }

    function decimals() external view returns (uint8) {
        return feedDecimals;
    }

    function latestRoundData() external view returns (uint80, int256, uint256, uint256, uint80) {
        return (roundId, answer, startedAt, updatedAt, answeredInRound);
    }
}

contract SequencerGateTest is Test {
    GateAdmin private admin;
    SequencerGate private gate;
    MockSequencerFeed private feed;

    function setUp() public {
        vm.warp(10_000);
        admin = new GateAdmin();
        gate = new SequencerGate(address(admin));
        feed = new MockSequencerFeed();
    }

    function testUnboundGateReportsDown() public view {
        (
            uint80 roundId,
            int256 answer,
            uint256 startedAt,
            uint256 updatedAt,
            uint80 answeredInRound
        ) = gate.latestRoundData();

        assertEq(gate.decimals(), 0);
        assertEq(address(gate.source()), address(0));
        assertEq(gate.expectedSourceCodeHash(), bytes32(0));
        assertEq(roundId, 0);
        assertEq(answer, 1);
        assertEq(startedAt, block.timestamp);
        assertEq(updatedAt, block.timestamp);
        assertEq(answeredInRound, 0);
    }

    function testRejectsEOAConfigAdmin() public {
        vm.expectRevert(SequencerGate.InvalidConfigAdmin.selector);
        new SequencerGate(makeAddr("eoa-admin"));
    }

    function testBindsOnceAndForwardsExactResponse() public {
        bytes32 codeHash = address(feed).codehash;
        vm.expectEmit(true, true, false, true, address(gate));
        emit SequencerGate.SourceBound(address(feed), codeHash);
        vm.prank(address(admin));
        gate.bindSource(feed, codeHash);

        assertEq(address(gate.source()), address(feed));
        assertEq(gate.expectedSourceCodeHash(), codeHash);
        (
            uint80 roundId,
            int256 answer,
            uint256 startedAt,
            uint256 updatedAt,
            uint80 answeredInRound
        ) = gate.latestRoundData();
        assertEq(roundId, 7);
        assertEq(answer, 0);
        assertEq(startedAt, block.timestamp - 1 hours);
        assertEq(updatedAt, block.timestamp);
        assertEq(answeredInRound, 7);

        vm.prank(address(admin));
        vm.expectRevert(SequencerGate.AlreadyBound.selector);
        gate.bindSource(feed, codeHash);
    }

    function testRejectsUnauthorizedBinding() public {
        vm.expectRevert(SequencerGate.NotConfigAdmin.selector);
        gate.bindSource(feed, address(feed).codehash);
    }

    function testRejectsInvalidSourceAndCodeHash() public {
        vm.startPrank(address(admin));
        vm.expectRevert(SequencerGate.InvalidSource.selector);
        gate.bindSource(gate, address(gate).codehash);
        vm.expectRevert(SequencerGate.InvalidSource.selector);
        gate.bindSource(IChainlinkFeed(address(1)), bytes32(uint256(1)));
        vm.expectRevert(SequencerGate.InvalidSource.selector);
        gate.bindSource(feed, bytes32(0));
        vm.expectRevert(SequencerGate.InvalidSource.selector);
        gate.bindSource(feed, bytes32(uint256(1)));
        vm.stopPrank();
    }

    function testRejectsNonSequencerResponseAtBinding() public {
        feed.setDecimals(8);
        vm.prank(address(admin));
        vm.expectRevert(SequencerGate.InvalidSourceResponse.selector);
        gate.bindSource(feed, address(feed).codehash);

        feed.setDecimals(0);
        feed.setRound(2, block.timestamp - 1, block.timestamp);
        vm.prank(address(admin));
        vm.expectRevert(SequencerGate.InvalidSourceResponse.selector);
        gate.bindSource(feed, address(feed).codehash);

        feed.setRound(0, 0, block.timestamp);
        vm.prank(address(admin));
        vm.expectRevert(SequencerGate.InvalidSourceResponse.selector);
        gate.bindSource(feed, address(feed).codehash);
    }

    function testRevertsWhenSourceCodeChanges() public {
        bytes32 codeHash = address(feed).codehash;
        vm.prank(address(admin));
        gate.bindSource(feed, codeHash);

        vm.etch(address(feed), hex"00");
        bytes32 actual = address(feed).codehash;
        vm.expectRevert(
            abi.encodeWithSelector(SequencerGate.SourceCodeChanged.selector, codeHash, actual)
        );
        gate.latestRoundData();
    }

    function testRejectsInvalidResponseAfterBinding() public {
        vm.prank(address(admin));
        gate.bindSource(feed, address(feed).codehash);

        feed.setRound(2, block.timestamp - 1, block.timestamp);
        vm.expectRevert(SequencerGate.InvalidSourceResponse.selector);
        gate.latestRoundData();

        feed.setRound(0, block.timestamp + 1, block.timestamp);
        vm.expectRevert(SequencerGate.InvalidSourceResponse.selector);
        gate.latestRoundData();
    }
}
