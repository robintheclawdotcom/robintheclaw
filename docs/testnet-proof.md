# Testnet proof deployment

## Purpose

The Robinhood testnet deployment proves the narrow path that is currently safe to prove:

```text
agent -> StrategyVault -> AttestationAnchor -> public verifier
```

It does not execute a swap, open a perpetual, move user funds, or claim an investment result.
The committed record is a disclosed synthetic fixture labeled as such.

## Addresses

The authoritative values are in [`deployments/testnet-proof.json`](../deployments/testnet-proof.json).
They include a `tUSDG` fixture asset, guard, vault, anchor, role addresses, deployment transaction,
proof transaction, and root. The deployment is on Robinhood Chain testnet, chain ID 46630.

## Verification procedure

```sh
cd verifier
npm ci
npm run verify:testnet-proof
```

The verifier checks:

1. Vault asset, guard, owner, agent, and anchor against the deployment record.
2. Guard owner and executor against the owner and vault.
3. Anchor publisher against the vault.
4. Fixture kind and canonical Merkle root.
5. On-chain sequence, root, and trade count.

Any mismatch exits nonzero. The command is read-only and requires no key.

## Why the fixture is synthetic

No canonical testnet USDG or execution venue has been verified. Inventing one would make the test
look more complete while weakening the guarantee. The fixture gives the public a reproducible
anchor/verifier result without implying a real market fill.
