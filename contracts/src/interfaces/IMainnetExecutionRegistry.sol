// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

interface IMainnetExecutionRegistry {
    enum Mode {
        Active,
        ReduceOnly,
        Halted
    }

    function configAdmin() external view returns (address);
    function guardian() external view returns (address);
    function globalMode() external view returns (Mode);
    function isFactoryApproved(address factory) external view returns (bool);
    function isRegisteredVault(address vault) external view returns (bool);

    function registerGraph(address owner, address vault, address riskManager, address spotAdapter)
        external;
}
