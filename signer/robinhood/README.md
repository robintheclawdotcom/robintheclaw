# Robinhood writer

Private, fail-closed Robinhood Chain transaction writer. The service accepts one fixed operation:
`RwaUserStrategyVaultV1.executeSpot(SpotIntent)`. It has no target, calldata, transfer, withdrawal,
recovery, market-configuration, agent-management, or governance API.

Every request carries an opaque `execution_account_id`. The signer resolves the account through the
private Robinhood provisioner using a dedicated HMAC key with replay protection and authenticated
responses. Static account files, raw KMS references, vault addresses, and signer addresses are not
accepted from callers or environment configuration.

Before each new signature, the provisioner and signer independently verify the active KMS key,
chain ID, factory, registry, policy digest, graph runtime hashes, owner, agent, risk manager, and
adapter wiring through independent RPC providers. Any changed binding latches the writer unready and
requires reconciliation plus a signer restart. The KMS public key must recover to the provisioned
agent address.

Nonce advancement and signed-transaction persistence commit atomically. The reconciler validates
every stored transaction before broadcast, follows fee-replacement candidates until a canonical
winner is known, and requires both providers to agree on receipt, `safe`, and `finalized` evidence.
Invalid records are quarantined and keep the signer unready.

The service is disabled unless `ROBINHOOD_SIGNER_ENABLED=true`. Production also requires a migrated
PostgreSQL journal, private independent RPC endpoints, and the provisioner bridge. See
[`docs/execution-control-plane.md`](../../docs/execution-control-plane.md).
