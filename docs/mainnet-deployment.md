# Mainnet contract deployment

Robin's original typed singleton contract graph is deployed on Robinhood Chain mainnet, chain ID
4663. The onchain state below is a historical snapshot of that operator-only canary graph: it is
halted and unfunded, and it is never used for customer capital.

The current live lane uses `RwaUserVaultFactoryV1` and `MainnetExecutionRegistry` to create an
isolated non-upgradeable vault, risk manager, adapter, anchor, and execution-key binding per user.
The factory release and mainnet services are enabled in code. An owner or an ordinary ETH-funded
relayer may deploy the graph; paymaster sponsorship is not required.

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
| Role in current release | Historical operator canary only |

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
set must be replaced with device-separated operational owners before this singleton receives capital. The
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

## Internal review evidence

The deployment release passed 60 Foundry tests, including fuzz coverage of the buy-side accounting
invariant and focused tests for authorization, governance, recovery, replay, slippage, fresh marking,
corporate actions, stale feeds, sequencer failure, code-hash pinning, allowance cleanup, route
direction, reduce-only exits, and fee-on-transfer rejection. An internal manual review and Slither
triage found no confirmed permissionless critical or high-severity issue in the deployed typed path.

The repository's internal audit must close and retest every critical or high contract, executor,
and key finding against the exact release commit.

## Per-user live activation

The current customer lane activates one isolated account at a time:

1. deploy the approved factory and registry release with pinned router, Permit2, token, pool,
   bytecode, oracle, sequencer, and risk-policy digests;
2. create the account's non-exportable KMS execution key;
3. let the owner or an ETH-funded relayer deterministically deploy the per-user graph;
4. have the owner authorize the fixed agent and deposit USDG while funding the user-owned Lighter
   subaccount separately with USDC;
5. fund the execution signer with gas and verify every canonical graph and account binding;
6. close and retest the internal contract, executor, and key review for the exact release; and
7. launch when fresh quotes, authenticated venue state, margin, route, oracle, sequencer, nonce,
   reconciliation, and control checks all pass.

Every owner retains immediate restriction, revocation, and flat-state withdrawal authority.
