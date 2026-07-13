# Robinhood writer

Private, fail-closed Robinhood Chain transaction writer. The service accepts one fixed operation:
`RwaStrategyVault.executeSpot(SpotIntent)`. It has no target, calldata, transfer, withdrawal,
recovery, market-configuration, agent-management, or governance API.

Production mode requires independent primary and reconciliation RPC providers, a non-exportable
AWS KMS `ECC_SECG_P256K1` signing key, a migrated PostgreSQL journal, and a pinned deployment
manifest. Startup and every new signature verify the KMS address, chain ID, runtime code hashes,
vault wiring, settlement asset, timelock, recovery Safe, and guardian through both providers.

Nonce advancement and signed-transaction persistence commit atomically. The reconciler validates
every stored transaction before broadcast, follows every fee-replacement candidate until a
canonical winner is known, and requires both providers to agree on receipt, `safe`, and
`finalized` evidence. Invalid records are quarantined and keep the signer unready.

The write route uses timestamped HMAC service authentication with persistent replay protection.
It exposes no raw target, calldata, transfer, withdrawal, or governance operation. The service is
disabled unless `ROBINHOOD_SIGNER_ENABLED=true`. See
[`docs/execution-control-plane.md`](../../docs/execution-control-plane.md).
