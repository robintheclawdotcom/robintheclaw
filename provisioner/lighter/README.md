# Lighter credential provisioner

The provisioner owns envelope-encrypted Lighter credentials. Product callers can prepare and confirm owner-authorized key associations. The transaction signer can request account-bound signatures. The account publisher can request read-only state using only an `executionAccountId`. No route accepts or returns an Ethereum private key, Lighter private key, withdrawal, or transfer request.

Link preparation accepts only the execution-account ID, owner address, and server-pinned API-key index. The provisioner discovers every account for the owner through Lighter's mainnet API, excludes the minimum-index master account, and accepts exactly one account-type-1 subaccount only after both summary and detailed account responses show no orders, positions, collateral, or asset balance. It fetches the association nonce from `nextNonce`; callers cannot supply either account index or nonce. No eligible account and multiple eligible accounts both fail closed with a create-or-resolve instruction.

Terminal close freezes signing before it reserves a purpose-separated tombstone credential. The owner signs the Lighter key-change payload through `/v1/links/revoke/confirm`; the provisioner does not erase the active or tombstone secrets until Lighter reports that exact tombstone as the registered key. Ambiguous broadcasts stay reconcilable, unexpected keys block the account, and expired unsigned payloads are replaced without reopening signing.

Every authenticated response is HMAC-bound to the request path, caller, nonce, HTTP status, and response-body digest. Callers reject unsigned, oversized, or mismatched responses before parsing venue evidence.

The publisher bridge requires a caller and 32-byte hex HMAC key distinct from the product and signer credentials:

- `LIGHTER_PUBLISHER_BRIDGE_CALLER_ID=account-publisher`
- `LIGHTER_PUBLISHER_BRIDGE_HMAC_KEY`
- `LIGHTER_PUBLISHER_MARKET_ID` pinned to the approved AAPL perpetual
- optional `LIGHTER_PUBLISHER_MAX_REQUESTS_PER_MINUTE` and `LIGHTER_PUBLISHER_MAX_CONCURRENT`

For each authenticated request, the service verifies the active database binding and registered Lighter public key, decrypts the credential with account-bound KMS context, derives expected nonce from the key-association nonce and append-only signed-transaction audit, and creates a short-lived auth token only in process. It validates account identity on account, order, trade, and nonce responses before returning public evidence. Credential rotation or blocking makes the route fail closed.

The account publisher uses the same caller ID and HMAC value under those exact environment names. The two Render services must receive the same secret value; it must differ from every product, coordinator, application, and signer HMAC.

The bridge uses the existing durable `lighter_provisioner_request_nonces` table for replay protection and requires no schema migration.

Production migration jobs set `LIGHTER_PROVISIONER_RUN_MIGRATIONS=true`. Runtime instances set it to `false` after the ordered migration job succeeds.

```bash
go test ./...
go vet ./...
```
