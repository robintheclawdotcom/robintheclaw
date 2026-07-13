// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import { Script, console2 } from "forge-std/Script.sol";
import { TimelockController } from "@openzeppelin/contracts/governance/TimelockController.sol";

interface ISafeProxy {
    function masterCopy() external view returns (address);
}

interface ISafeAccount {
    function VERSION() external view returns (string memory);

    function getOwners() external view returns (address[] memory);

    function getThreshold() external view returns (uint256);
}

contract DeployGovernance is Script {
    address private constant SAFE_L2 = 0xEdd160fEBBD92E350D4D398fb636302fccd67C7e;
    bytes32 private constant SAFE_L2_CODE_HASH =
        0x180193227186ccb85316c94db1f0d156ed932b14712cfaac78901899178572dc;

    function run() external {
        address safe = vm.envAddress("SAFE");
        uint256 delay = vm.envUint("TIMELOCK_DELAY");

        require(block.chainid == 4663, "unexpected chain");
        require(safe.code.length > 0, "safe has no code");
        require(safe.codehash == vm.envBytes32("SAFE_PROXY_CODEHASH"), "Safe codehash mismatch");
        require(delay > 0, "timelock delay=0");
        require(SAFE_L2.codehash == SAFE_L2_CODE_HASH, "SafeL2 code changed");
        require(ISafeProxy(safe).masterCopy() == SAFE_L2, "unexpected Safe singleton");
        require(
            keccak256(bytes(ISafeAccount(safe).VERSION())) == keccak256(bytes("1.5.0")),
            "unexpected Safe version"
        );
        address[] memory owners = ISafeAccount(safe).getOwners();
        address owner1 = vm.envAddress("SAFE_OWNER_1");
        address owner2 = vm.envAddress("SAFE_OWNER_2");
        address owner3 = vm.envAddress("SAFE_OWNER_3");
        require(owners.length == 3, "Safe owner count != 3");
        require(ISafeAccount(safe).getThreshold() == 2, "Safe threshold != 2");
        require(
            owner1 != address(0) && owner2 != address(0) && owner3 != address(0) && owner1 != owner2
                && owner1 != owner3 && owner2 != owner3,
            "invalid expected Safe owners"
        );
        require(
            _contains(owners, owner1) && _contains(owners, owner2) && _contains(owners, owner3),
            "Safe owners mismatch"
        );

        address[] memory proposers = new address[](1);
        proposers[0] = safe;
        address[] memory executors = new address[](1);
        executors[0] = safe;

        vm.startBroadcast();
        TimelockController timelock =
            new TimelockController(delay, proposers, executors, address(0));
        vm.stopBroadcast();

        require(timelock.getMinDelay() == delay, "delay mismatch");
        require(timelock.hasRole(timelock.PROPOSER_ROLE(), safe), "Safe not proposer");
        require(timelock.hasRole(timelock.CANCELLER_ROLE(), safe), "Safe not canceller");
        require(timelock.hasRole(timelock.EXECUTOR_ROLE(), safe), "Safe not executor");
        require(!timelock.hasRole(timelock.EXECUTOR_ROLE(), address(0)), "open executor");
        require(
            timelock.hasRole(timelock.DEFAULT_ADMIN_ROLE(), address(timelock)),
            "timelock not self-admin"
        );
        require(!timelock.hasRole(timelock.DEFAULT_ADMIN_ROLE(), safe), "Safe is admin");

        console2.log("Safe", safe);
        console2.log("Timelock", address(timelock));
        console2.log("Delay", delay);
    }

    function _contains(address[] memory values, address expected) private pure returns (bool) {
        for (uint256 i; i < values.length; ++i) {
            if (values[i] == expected) return true;
        }
        return false;
    }
}
