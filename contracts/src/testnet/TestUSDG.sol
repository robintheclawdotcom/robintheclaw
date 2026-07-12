// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { ERC20 } from "@openzeppelin/contracts/token/ERC20/ERC20.sol";

/// @notice Test-only asset for exercising the custody and attestation path on Robinhood testnet.
/// It is not USDG and is never a valid mainnet deployment asset.
contract TestUSDG is ERC20 {
    uint8 private constant DECIMALS = 6;

    constructor(address recipient, uint256 initialSupply)
        ERC20("Robin the Claw Test Dollar", "tUSDG")
    {
        _mint(recipient, initialSupply);
    }

    function decimals() public pure override returns (uint8) {
        return DECIMALS;
    }
}
