# Contracts

`MandateGuard` and `StrategyVault` are the capital-control layer for a profitable trading agent.
They bound each execution to an approved target and selector, a rolling notional cap, and a kill
switch. `AttestationAnchor` stores append-only Merkle roots; the vault is its only publisher, so
the agent anchors realized results through the same custody boundary.

This is not a public deposit vault. It has no share or NAV accounting and must not receive funds
from anyone other than its configured owner.

## Local checks

```sh
forge fmt --check
forge test -vvv
forge build --sizes
```

## Deployment gate

`script/Deploy.s.sol` requires `OWNER`, `AGENT`, and `ASSET`. It never supplies an asset or venue
address by default. `UNIVERSAL_ROUTER` is optional; when omitted, the deployed vault has no
execution venue allowlisted. Supply only chain-verified values and deploy with the owner key:

```sh
OWNER=0x... AGENT=0x... ASSET=0x... \
forge script script/Deploy.s.sol:Deploy --rpc-url "$RPC_URL" --private-key "$OWNER_KEY" --broadcast
```

The testnet asset and router are deliberately unconfigured in `config/addresses.json`. Do not
replace that gate with an assumed address.

## Isolated testnet proof path

`script/DeployTestnet.s.sol` deploys a clearly named `tUSDG` test asset and a vault with no
allowlisted venue. It exists to verify owner/agent roles, custody, and on-chain attestations; it
cannot execute a trade. Use it only on Robinhood testnet and record its addresses in a local,
ignored deployment file.
