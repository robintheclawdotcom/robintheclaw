# Lighter signer

The signer wraps the pinned official `lighter-go` implementation and exposes only order, cancel,
dead-man cancellation, and short-lived authentication operations. It cannot sign withdrawals,
transfers, leverage changes, account administration, or subaccount operations.

The service is disabled unless `LIGHTER_SIGNER_ENABLED=true`. Production also requires a private
service token, dedicated API key, account index, API-key index, and Lighter chain ID. It accepts an
explicit nonce for every transaction; the execution coordinator owns the durable nonce journal.

The service must run on a private network with a collateral-capped subaccount. Never place an EVM
private key in this service.

```bash
go test ./...
```
