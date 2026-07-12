// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IAttestationAnchor } from "./interfaces/IAttestationAnchor.sol";

/// @title AttestationAnchor
/// @notice Append-only registry of Merkle roots committing an agent's trade-log batches.
///         Anyone reads a root and recomputes the record off-chain; the publisher can only
///         append in strict sequence, never rewrite history. One anchor per strategy vault.
contract AttestationAnchor is IAttestationAnchor {
    struct Batch {
        bytes32 root;
        uint64 sequence;
        uint64 tradeCount;
        uint64 timestamp;
    }

    address public immutable publisher;

    mapping(uint64 => Batch) public batches;
    uint64 public head;

    event RootAnchored(uint64 indexed sequence, bytes32 root, uint64 tradeCount, uint64 timestamp);

    error NotPublisher();
    error BadSequence(uint64 expected, uint64 got);
    error EmptyRoot();

    constructor(address publisher_) {
        require(publisher_ != address(0), "publisher=0");
        publisher = publisher_;
    }

    function anchor(bytes32 root, uint64 sequence, uint64 tradeCount) external override {
        if (msg.sender != publisher) revert NotPublisher();
        if (root == bytes32(0)) revert EmptyRoot();
        uint64 expected = head + 1;
        if (sequence != expected) revert BadSequence(expected, sequence);

        batches[sequence] = Batch(root, sequence, tradeCount, uint64(block.timestamp));
        head = sequence;
        emit RootAnchored(sequence, root, tradeCount, uint64(block.timestamp));
    }

    function latest() external view returns (Batch memory) {
        return batches[head];
    }
}
