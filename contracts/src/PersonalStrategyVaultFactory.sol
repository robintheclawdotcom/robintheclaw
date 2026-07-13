// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { PersonalStrategyVault } from "./PersonalStrategyVault.sol";

/// @notice Deploys one deterministic personal strategy vault per owner for this factory version.
contract PersonalStrategyVaultFactory {
    uint64 public constant VERSION = 1;

    IERC20 public immutable asset;
    address public immutable defaultAgent;
    uint256 public immutable defaultCap;
    uint64 public immutable defaultWindow;

    mapping(address => address) public vaultOf;

    event VaultCreated(
        address indexed owner,
        address indexed vault,
        address guard,
        address anchor,
        address asset,
        uint64 version
    );

    error InvalidConfiguration();
    error VaultExists(address vault);
    error UnexpectedAddress(address expected, address actual);

    constructor(IERC20 asset_, address defaultAgent_, uint256 defaultCap_, uint64 defaultWindow_) {
        if (address(asset_) == address(0) || defaultAgent_ == address(0)) {
            revert InvalidConfiguration();
        }
        if (defaultCap_ == 0 || defaultWindow_ == 0) revert InvalidConfiguration();

        asset = asset_;
        defaultAgent = defaultAgent_;
        defaultCap = defaultCap_;
        defaultWindow = defaultWindow_;
    }

    function predictVault(address owner) public view returns (address) {
        bytes32 initCodeHash = keccak256(
            abi.encodePacked(
                type(PersonalStrategyVault).creationCode,
                abi.encode(asset, owner, defaultAgent, defaultCap, defaultWindow)
            )
        );
        return address(
            uint160(
                uint256(
                    keccak256(
                        abi.encodePacked(bytes1(0xff), address(this), _salt(owner), initCodeHash)
                    )
                )
            )
        );
    }

    function createVault() external returns (address vault) {
        address existing = vaultOf[msg.sender];
        if (existing != address(0)) revert VaultExists(existing);

        address expected = predictVault(msg.sender);
        PersonalStrategyVault deployed = new PersonalStrategyVault{ salt: _salt(msg.sender) }(
            asset, msg.sender, defaultAgent, defaultCap, defaultWindow
        );
        vault = address(deployed);
        if (vault != expected) revert UnexpectedAddress(expected, vault);

        vaultOf[msg.sender] = vault;
        emit VaultCreated(
            msg.sender,
            vault,
            address(deployed.guard()),
            address(deployed.attestationAnchor()),
            address(asset),
            VERSION
        );
    }

    function _salt(address owner) private pure returns (bytes32) {
        return keccak256(abi.encode(owner, VERSION));
    }
}
