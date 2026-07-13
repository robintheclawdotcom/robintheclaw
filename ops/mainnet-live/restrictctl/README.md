# Operator restriction control

`restrictctl` can only make the global, strategy, or registered-account execution control more
restrictive. It permits `ACTIVE` to move to `REDUCE_ONLY` or `HALTED`, and `REDUCE_ONLY` to move to
`HALTED`. It cannot activate, unhalt, submit an intent, contact a signer, or move funds.

Every change uses a serializable transaction and an expected control version. The append-only event
binds the exact scope, strategy and account identities, reason, evidence digest, operator identity,
and resulting version. Requests are domain-separated and signed with Ed25519. Retrying the same
request ID and canonical digest is idempotent; any collision fails closed.

The database connection and signing inputs are environment-only:

```text
RESTRICTCTL_DATABASE_URL
RESTRICTCTL_OPERATOR_ID
RESTRICTCTL_PRIVATE_KEY_FILE
RESTRICTCTL_PUBLIC_KEY_FILE
```

The private key must be an owner-only regular file containing one PKCS#8 Ed25519 PEM block. The
public key file contains the matching PKIX public key. Use a dedicated database role limited to
selecting registered identities and controls, updating the three control tables, and inserting and
reading `execution_operator_restriction_events`.

Apply an account restriction:

```sh
go run ./cmd/restrictctl \
  --request-id ops-20260714-account-0001 \
  --scope account \
  --strategy-version basis-aapl-v1 \
  --execution-account-id account-00000001 \
  --expected-version 0 \
  --from ACTIVE \
  --to REDUCE_ONLY \
  --reason 'operator pause pending reconciliation' \
  --evidence-sha256 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
```

For global scope, omit both identity flags. For strategy scope, supply only `--strategy-version`.
The command emits the request digest, resulting mode and version, and whether the result was an
idempotent replay. It never emits key material or database configuration.
