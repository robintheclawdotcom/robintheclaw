# Contracts

`MandateGuard` and `StrategyVault` are the capital-control layer for the trading system. They bind
each execution to an approved target and selector, a rolling notional cap, and a kill switch.
`AttestationAnchor` stores append-only Merkle roots; the vault is its only publisher, so disclosed
results are anchored through the same custody boundary.

This is not a public deposit vault. It has no share or NAV accounting and must not receive funds
from anyone other than its configured owner.

## Local checks

```sh
forge fmt --check
forge test -vvv
forge build --sizes
```

## Deployment gate

`script/Deploy.s.sol` requires explicit `OWNER`, `AGENT`, `ASSET`, `WINDOW_CAP`, and
`WINDOW_SECONDS` values. It deploys a halted core with no allowlisted venue. The generic router
path is deliberately disabled: a typed, venue-specific adapter is required before any execution
can be enabled. Supply only chain-verified values and deploy with the owner key:

```sh
OWNER=0x... AGENT=0x... ASSET=0x... WINDOW_CAP=... WINDOW_SECONDS=... \
forge script script/Deploy.s.sol:Deploy --rpc-url "$RPC_URL" --private-key "$OWNER_KEY" --broadcast
```

After confirmation, verify every role, reference, limit, and the halted state before recording a
deployment:

```sh
OWNER=0x... AGENT=0x... ASSET=0x... WINDOW_CAP=... WINDOW_SECONDS=... \
MANDATE_GUARD=0x... STRATEGY_VAULT=0x... ATTESTATION_ANCHOR=0x... \
forge script script/VerifyDeployment.s.sol:VerifyDeployment --rpc-url "$RPC_URL"
```

The testnet asset and router are deliberately unconfigured in `config/addresses.json`. Do not
replace that gate with an assumed address.

## Isolated testnet proof path

`script/DeployTestnet.s.sol` deploys a clearly named `tUSDG` test asset and a vault with no
allowlisted venue. It exists to verify owner/agent roles, custody, and onchain attestations; it
cannot execute a trade. Use it only on Robinhood testnet and record its addresses in a local,
ignored deployment file.
