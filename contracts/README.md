# Contracts

The production contract layer is a typed, non-upgradeable execution boundary for Robin's
delta-neutral strategy. It is deployed on Robinhood Chain mainnet behind a canonical 2-of-3 Safe
and a 48-hour OpenZeppelin timelock. The deployment launched halted and unfunded with the agent set
to zero and no market, route, or sequencer source configured.

`RwaStrategyVault` holds USDG and exposes only typed spot execution. `MandateRiskManagerV1`
authorizes each intent against replay, time, oracle, multiplier, slippage, inventory, turnover,
fresh gross-exposure, and operating-mode constraints. `UniswapV4SpotAdapter` constructs the reviewed
zero-hook route internally and pins the canonical Universal Router and Permit2 runtime code hashes.
`SequencerGate` fails closed until the timelock binds a reviewed source. `AttestationAnchor` stores
append-only Merkle roots with the vault as its only publisher.

The proof and consumer contracts remain separate. The consumer application uses
`PersonalStrategyVaultFactory` to create one deterministic vault per smart-account owner and
factory version. A personal vault deploys its guard and anchor in its constructor, accepts funding
from linked wallets, and keeps withdrawal and strategy control with its owner. It has no pooled
shares or pooled NAV accounting.

## Production contract graph

| Contract | Responsibility |
| --- | --- |
| `RwaDeploymentFactory` | Deploys and permanently records the bound v1 contract graph. |
| `SequencerGate` | Forwards a one-time-bound sequencer source and fails closed before binding. |
| `MandateRiskManagerV1` | Enforces typed intent, market, oracle, multiplier, exposure, turnover, and mode policy. |
| `UniswapV4SpotAdapter` | Builds one configured exact-input route and cleans every temporary allowance. |
| `RwaStrategyVault` | Holds USDG, measures actual balance deltas, executes typed spot intents, and supports terminal recovery. |
| `AttestationAnchor` | Stores append-only strategy-record roots published by the vault. |

The canonical addresses, deployment transactions, runtime code hashes, limits, and activation
state are recorded in [`deployments/mainnet.json`](../deployments/mainnet.json) and explained in
[`docs/mainnet-deployment.md`](../docs/mainnet-deployment.md).

## Local checks

```sh
forge fmt --check
forge test -vvv
forge build --sizes
```

## Production deployment

`script/DeployGovernance.s.sol` creates the timelock after verifying the canonical Safe singleton,
proxy code hash, version, threshold, and exact expected owner set. `script/Deploy.s.sol` then
verifies governance roles, external runtime code hashes, USDG decimals, chain ID, and bounded limit
types before creating the v1 graph. The initial agent is always zero.

```sh
TIMELOCK=0x... SAFE=0x... GUARDIAN=0x... \
ASSET=0x... UNIVERSAL_ROUTER=0x... PERMIT2=0x... \
TIMELOCK_CODEHASH=0x... SAFE_PROXY_CODEHASH=0x... \
UNIVERSAL_ROUTER_CODEHASH=0x... PERMIT2_CODEHASH=0x... \
TIMELOCK_DELAY=172800 GROSS_NOTIONAL_LIMIT=... TURNOVER_LIMIT=... \
TURNOVER_WINDOW=86400 MAX_DEADLINE_DELAY=300 \
SEQUENCER_GRACE_PERIOD=3600 MAX_ACTIVE_MARKETS=1 \
forge script script/Deploy.s.sol:Deploy \
  --rpc-url "$ROBINHOOD_MAINNET_RPC" \
  --private-key "$DEPLOYER_KEY" \
  --broadcast --slow
```

The deployer receives no contract role. After confirmation, derive expected runtime code hashes
from the reproducible release artifacts and run the read-only verifier:

```sh
RWA_DEPLOYMENT_FACTORY=0x... SEQUENCER_GATE=0x... \
MANDATE_RISK_MANAGER=0x... UNISWAP_V4_SPOT_ADAPTER=0x... \
RWA_STRATEGY_VAULT=0x... ATTESTATION_ANCHOR=0x... \
forge script script/VerifyDeployment.s.sol:VerifyDeployment \
  --rpc-url "$ROBINHOOD_MAINNET_RPC"
```

The verifier checks factory-child provenance, Safe and timelock roles, every internal reference,
runtime code hashes, external contract hashes, initial limits, zero balances, zero agent, halted
mode, empty inventory, and the unbound fail-closed sequencer gate. Passing verification records a
deployment; it does not authorize capital activation.

## Governance and recovery

- The timelock installs an agent, loosens mode, configures markets and routes, changes limits, binds
  the sequencer source, and rotates the guardian.
- The Safe funds and recovers the vault, immediately revokes the agent, and can restrict mode.
- The guardian can only move from `ACTIVE` to `REDUCE_ONLY` or `HALTED` and cannot loosen policy.
- Final recovery is terminal: it requires `HALTED`, clears the agent, prevents later deposits or
  execution, and permits Safe-controlled token sweeps.
- The adapter grants exact temporary approvals, resets ERC-20 and Permit2 allowances, retains no
  funds, and rejects unexpected input or output deltas.

The mainnet Safe owner set must be rotated from bootstrap custody to device-separated operational
owners before capital activation.

## Isolated testnet proof path

`script/DeployTestnet.s.sol` deploys a clearly named `tUSDG` test asset and a vault with no
allowlisted venue. It exists to verify owner/agent roles, custody, and onchain attestations; it
cannot execute a trade. Use it only on Robinhood testnet and record its addresses in a local,
ignored deployment file.

## Personal-vault testnet path

`script/DeployUxTestnet.s.sol` deploys `tUSDG`, `TestAssetFaucet`, and
`PersonalStrategyVaultFactory` on chain ID 46630. The faucet permits one fixed claim per smart
account. The factory uses CREATE2, rejects duplicate creation, and emits every child contract
address needed by the application receipt verifier.
