# Lighter signer

The signer wraps the pinned official `lighter-go` implementation and exposes only order, cancel,
dead-man cancellation, and short-lived authentication operations. It cannot sign withdrawals,
transfers, leverage changes, account administration, or subaccount operations.

The service is disabled unless `LIGHTER_SIGNER_ENABLED=true`. Production also requires a private
service token and an owner-only account registry selected with `LIGHTER_SIGNER_ACCOUNTS_FILE`.
Each registry entry binds one opaque execution-account ID to a dedicated API private key, chain ID,
account index, and API-key index. Requests contain the execution-account ID; the signer resolves
and verifies the credential and returns the resolved public account identity. The coordinator
never receives private keys.

The legacy single-account configuration additionally requires
`LIGHTER_EXECUTION_ACCOUNT_ID`. It accepts an explicit nonce for every transaction; the execution
coordinator owns the durable account-scoped nonce journal.

The service must run on a private network with a collateral-capped subaccount. Never place an EVM
private key in this service.

```bash
go test ./...
```
