// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IChainlinkFeed } from "./interfaces/IChainlinkFeed.sol";

contract SequencerGate is IChainlinkFeed {
    address public immutable configAdmin;
    IChainlinkFeed public source;
    bytes32 public expectedSourceCodeHash;

    event SourceBound(address indexed source, bytes32 indexed codeHash);

    error InvalidConfigAdmin();
    error NotConfigAdmin();
    error AlreadyBound();
    error InvalidSource();
    error InvalidSourceResponse();
    error SourceCodeChanged(bytes32 expected, bytes32 actual);

    constructor(address configAdmin_) {
        if (configAdmin_.code.length == 0) revert InvalidConfigAdmin();
        configAdmin = configAdmin_;
    }

    function bindSource(IChainlinkFeed source_, bytes32 codeHash_) external {
        if (msg.sender != configAdmin) revert NotConfigAdmin();
        if (address(source) != address(0)) revert AlreadyBound();
        if (
            address(source_) == address(this) || address(source_).code.length == 0
                || codeHash_ == bytes32(0) || address(source_).codehash != codeHash_
        ) revert InvalidSource();
        if (source_.decimals() != 0) revert InvalidSourceResponse();

        (, int256 answer, uint256 startedAt, uint256 updatedAt,) = source_.latestRoundData();
        if (
            (answer != 0 && answer != 1) || startedAt == 0 || startedAt > block.timestamp
                || updatedAt == 0 || updatedAt > block.timestamp
        ) revert InvalidSourceResponse();

        source = source_;
        expectedSourceCodeHash = codeHash_;
        emit SourceBound(address(source_), codeHash_);
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
        IChainlinkFeed source_ = source;
        if (address(source_) == address(0)) {
            return (0, 1, block.timestamp, block.timestamp, 0);
        }

        bytes32 expected = expectedSourceCodeHash;
        bytes32 actual = address(source_).codehash;
        if (actual != expected) revert SourceCodeChanged(expected, actual);

        (roundId, answer, startedAt, updatedAt, answeredInRound) = source_.latestRoundData();
        if (
            (answer != 0 && answer != 1) || startedAt == 0 || startedAt > block.timestamp
                || updatedAt == 0 || updatedAt > block.timestamp
        ) revert InvalidSourceResponse();
    }
}
