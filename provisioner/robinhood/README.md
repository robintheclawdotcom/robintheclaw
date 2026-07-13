# Robinhood account provisioner

Private authority for per-user Robinhood Chain execution bindings. It creates or resolves one AWS
KMS customer-managed `ECC_SECG_P256K1` signing key for each execution account and stores the KMS ARN
only in its private database. KMS keys use deterministic aliases and AWS KMS origin, so private key
material is never exportable.

`POST /v1/graphs/prepare` returns one unsigned, zero-value `RwaUserVaultFactoryV1.deploy(owner)` call.
The owner or any ordinary ETH-funded relayer may submit it. The route never signs, broadcasts, or
returns governance, transfer, withdrawal, or recovery calls. Gas sponsorship is not required.

`POST /v1/graphs/confirm` activates a binding only after two independent RPC providers agree that:

- the exact prepared factory call succeeded and passed the configured finality depth;
- factory, registry, policy, and component runtime hashes match the pinned release;
- factory prediction, factory state, and registry mappings identify the same graph and owner; and
- the vault agent is the account's KMS address and all vault, risk-manager, and adapter wiring agrees.

The factory deploys graphs with no agent. Setting the KMS agent is intentionally not part of the
permissionless action: the configured timelock must execute `MainnetExecutionRegistry.setVaultAgent`
before confirmation can pass. Owner consent through `enableAgent` remains a separate owner action.

The signer-only resolve route uses a distinct HMAC key, persistent nonce replay protection, and a
signed response. It returns a private KMS reference only for an `active` binding after repeating the
dual-RPC graph checks. `rotation_pending`, stale, mismatched, and unconfirmed bindings fail closed.

The service is disabled unless `ROBINHOOD_PROVISIONER_ENABLED=true`. It must remain disabled until
the audited chain-4663 factory release, policy digest, runtime hashes, independent RPC endpoints, and
timelock agent-binding procedure are available and configured.
