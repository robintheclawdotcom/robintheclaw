# Mainnet contract deployment

Robin's typed production contract system is deployed on Robinhood Chain mainnet, chain ID 4663.
The deployment is deliberately halted and unfunded. It establishes the final contract and
governance boundary without authorizing an agent, configuring a market, or placing capital at risk.

The canonical machine-readable record is
[`deployments/mainnet.json`](../deployments/mainnet.json). The factory deployment is
[source-verified on Blockscout](https://robinhoodchain.blockscout.com/address/0x0fd95bd301b65f4f6cc3354315793194941eef77),
and the deployment transaction is
[available onchain](https://robinhoodchain.blockscout.com/tx/0xe8b7ca77feaf117e287eab146d7e79bdef83737a93453534bc9077da0e0ac961).

## Deployment state

| Property | Value |
| --- | --- |
| Network | Robinhood Chain mainnet |
| Chain ID | `4663` |
| Deployment block | `8829911` |
| Deployment time | `2026-07-13T16:59:50Z` |
| Strategy mode | `HALTED` |
| Agent | Zero address; no execution authority installed |
| Vault settlement balance | `0 USDG` |
| Configured markets and routes | None |
| Sequencer gate | Unbound and reporting down |
| Activation stage | Staged |

The deployment entered Robinhood batch `19509`. Its EIP-4844 batch commitment transaction
[`0x12ff…fced`](https://etherscan.io/tx/0x12ff5f4df0152e1fcbefa96e22eec91e2f7ecaf02e439dcb2be69605016fbced)
is finalized on Ethereum. The post-deployment verifier also re-read every contract through the
mainnet RPC and confirmed provenance, governance, code hashes, limits, halt state, zero balances,
and the absence of an agent, market, route, or sequencer source.

## Governance

| Component | Address | Authority |
| --- | --- | --- |
| 2-of-3 Safe | [`0xac82…44fd`](https://robinhoodchain.blockscout.com/address/0xac82b88be9a7a35f869bc94611e0e36ed7d444fd) | Treasury recovery, immediate agent revocation, emergency restriction, timelock proposal, cancellation, and execution |
| 48-hour timelock | [`0xc488…46b5`](https://robinhoodchain.blockscout.com/address/0xc4887b9f12fe6f98c77de986ca8c4c17bf5b46b5) | Agent installation, unhalt, limit changes, market configuration, route configuration, sequencer binding, and guardian rotation |
| Guardian | `0x263e…8969` | Restrict to `REDUCE_ONLY` or `HALTED`; cannot activate, configure, recover, or withdraw |

The Safe is a canonical Safe v1.5.0 proxy with a threshold of two out of three. Its bootstrap owner
set must be replaced with device-separated operational owners before any capital activation. The
timelock is self-administered; the Safe is not a direct timelock administrator, and the executor role
is not open to the zero address.

## Contract graph

| Contract | Address | Initial state |
| --- | --- | --- |
| `RwaDeploymentFactory` | [`0x0fd9…ef77`](https://robinhoodchain.blockscout.com/address/0x0fd95bd301b65f4f6cc3354315793194941eef77) | Factory provenance root |
| `SequencerGate` | [`0x57df…b46d`](https://robinhoodchain.blockscout.com/address/0x57df357dec949eb2b5202143f1557db4eb38b46d) | Unbound; fails closed |
| `MandateRiskManagerV1` | [`0x1114…6a97`](https://robinhoodchain.blockscout.com/address/0x111449d0c87469e64b8b92ba384c129d70c36a97) | `HALTED`; one-market ceiling; no inventory |
| `UniswapV4SpotAdapter` | [`0xd111…d00c`](https://robinhoodchain.blockscout.com/address/0xd111f04a69714031ea5772534f24747c2f67d00c) | Bound to the vault; no route configured |
| `RwaStrategyVault` | [`0xce9a…3c96`](https://robinhoodchain.blockscout.com/address/0xce9aa8b21d0385a1a5df20d9a567c8cfe3ba3c96) | Agent zero; balance zero; recovery not finalized |
| `AttestationAnchor` | [`0xee78…b366`](https://robinhoodchain.blockscout.com/address/0xee78acd24b5b7a10885d92d863e41f3814b4b366) | Vault is the only publisher |

All six project contracts and the OpenZeppelin timelock are source-verified on Blockscout. The
adapter pins Robinhood's canonical Universal Router and Permit2 runtime code hashes. The vault
accepts only USDG as settlement collateral and always receives swap output itself.

## Enforced execution boundary

The deployed vault does not accept an arbitrary target, selector, recipient, route, notional, or
calldata payload. Its only trading entry point accepts a typed `SpotIntent`:

```solidity
struct SpotIntent {
    bytes32 id;
    address stockToken;
    Side side;
    uint128 amountIn;
    uint128 minAmountOut;
    uint64 deadline;
    uint64 configVersion;
    uint256 expectedUIMultiplier;
    uint80 minOracleRoundId;
}
```

The risk manager independently enforces replay protection, the current config version, sequencer
health and grace period, oracle round and heartbeat, oracle pause state, corporate-action multiplier
stability, per-market slippage, order and inventory limits, freshly marked gross exposure, turnover,
market count, and `ACTIVE`/`REDUCE_ONLY`/`HALTED` mode. The adapter constructs a single reviewed
zero-hook Uniswap v4 route internally, uses exact temporary approvals, and rejects unexpected
balance deltas. Entry and exit permissions are separate so a disabled entry route does not strand a
reviewed exit.

## Review evidence

The deployment release passed 60 Foundry tests, including fuzz coverage of the buy-side accounting
invariant and focused tests for authorization, governance, recovery, replay, slippage, fresh marking,
corporate actions, stale feeds, sequencer failure, code-hash pinning, allowance cleanup, route
direction, reduce-only exits, and fee-on-transfer rejection. An internal manual review and Slither
triage found no confirmed permissionless critical or high-severity issue in the deployed typed path.

This is not an independent external audit. The next activation stage includes an independent
contract audit and executor/key review with no open critical or high findings.

## Activation sequence

The contract layer begins from a zero-authority, zero-capital state. Its controlled activation
sequence is:

1. bind a reviewed official sequencer-health source through the timelock;
2. configure a reviewed stock-token oracle and zero-hook spot route;
3. install the KMS-backed execution agent through the timelock;
4. rotate the Safe to device-separated operational owners and complete the key review;
5. complete the independent contract and executor audits;
6. complete the required capture, shadow, statistical, capacity, recovery, and incident evidence;
7. obtain written legal and venue approval; and
8. issue a separate Safe-approved canary authorization and funding plan.

Each transition produces onchain or operational evidence before the next stage advances.
