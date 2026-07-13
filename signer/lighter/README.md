# Lighter signer

The signer exposes only order, cancel, dead-man cancellation, and short-lived authentication
operations. It cannot sign withdrawals, transfers, leverage changes, account administration,
generic transactions, or subaccount operations.

The service is disabled unless `LIGHTER_SIGNER_ENABLED=true`. It authenticates the execution
coordinator with `LIGHTER_SIGNER_HMAC_KEY` and delegates signing to the private credential
provisioner with a distinct `LIGHTER_SIGNER_BRIDGE_HMAC_KEY`. Both hops bind the method, path,
caller, timestamp, nonce, and body digest. Requests contain only the opaque execution-account ID
and operation fields.

The signer has no configuration path for Lighter private keys. The provisioner resolves the active
credential, verifies its registered public key, decrypts it with account-bound KMS context, signs,
zeroes the plaintext, and returns the account index, API-key index, and credential version. The
signer rejects any returned account, intent, key identity, or transaction-payload mismatch.

The service must run on a private network. The execution coordinator owns the durable,
account-scoped nonce journal.

```bash
go test ./...
```
