// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

interface IAttestationAnchor {
    function anchor(bytes32 root, uint64 sequence, uint64 tradeCount) external;
}
