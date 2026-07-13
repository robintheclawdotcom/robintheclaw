# Testnet foundation

## Purpose

The Robinhood testnet deployment establishes the first connected Robin contract path:

```text
agent -> StrategyVault -> AttestationAnchor -> public verifier
```

It connects the agent, custody, on-chain record, and developer tooling on Robinhood Chain. The
first committed record is a synthetic fixture, giving the system a clear starting point for the
next phase of venue integration.

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

## The first record

The fixture provides a stable, reproducible record for the contract and verifier pipeline while
venue integrations are developed. It is deliberately labeled synthetic so developers can build on
an accurate picture of the current foundation.
