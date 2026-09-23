// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

// A tiny contract used to demonstrate Sequent. deposit and withdraw share the
// balances mapping and the total counter, so their outcomes depend on ordering.
contract Vault {
    mapping(address => uint256) public balances;
    uint256 public total;

    function deposit() external {
        balances[msg.sender] += 1;
        total += 1;
    }

    function withdraw() external {
        balances[msg.sender] -= 1;
        total -= 1;
    }

    function balanceOf(address a) external view returns (uint256) {
        return balances[a];
    }
}
