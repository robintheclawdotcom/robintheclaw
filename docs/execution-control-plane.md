# Execution control plane

## Status

The execution services are capital-disabled by default. Their presence in the repository is not
an authorization to deploy contracts, enable signing, fund an account, or submit an order.
Activation remains subject to the promotion, audit, legal, key-review, and operating gates defined
by the mainnet-readiness program.

## Authority boundaries

The execution coordinator is the only service permitted to request signatures. It does not hold an
EVM private key or a Lighter key. Each signer exposes a narrow operation set and runs as a private
single-instance service.

| Component | Authority | Explicitly unavailable |
|---|---|---|
| Execution coordinator | Advance a durable paired-execution saga | Raw signing, withdrawals, governance |
| Robinhood writer | `RwaStrategyVault.executeSpot(SpotIntent)` | Arbitrary target or calldata, ETH transfer, recovery, configuration |
| Lighter signer | Create, modify, cancel, cancel-all, auth-token generation | Withdrawal and transfer operations, EVM access |
| KMS key | Sign Robinhood Chain transactions for one agent address | Key export, Lighter access, Safe administration |
| Safe and timelock | Recovery and delayed administration | Automated strategy decisions |
| Guardian | Halt and reduce-only transitions | Activation, withdrawal, limit increases |

The production Robinhood writer accepts only the ABI-fixed `executeSpot` selector. The destination
is the configured vault, transaction value is zero, and the signed payload is reconstructed from a
typed request. A journal record that does not reproduce the expected chain ID, signer, vault,
selector, calldata, intent ID, and fee policy is quarantined.

## Deployment binding

The writer derives a deployment identifier from the following manifest:

- chain ID and KMS-derived agent address;
- timelock, recovery Safe, and guardian addresses;
- vault, risk-manager, and adapter addresses;
- expected runtime code hashes for all three contracts.

The journal scopes requests, transaction hashes, replacement links, and reconciliation queries to
that identifier. Startup fails closed if the same chain and signer have unresolved transactions
from another deployment. This prevents a database reused after a key, vault, or contract rotation
from rebroadcasting stale transactions in a new security context.

Both RPC providers independently verify, at a pinned block:

- configured chain ID;
- contract runtime code hashes;
- vault agent, risk manager, adapter, timelock, recovery recipient, and settlement asset;
- risk-manager executor, administrator, guardian, and settlement asset;
- adapter vault, administrator, and settlement asset.

Every signed journal record stores the primary and secondary verification block numbers and hashes.
The primary provider is an authenticated production endpoint. The secondary provider is
reconciliation-only and must be operationally independent. Robinhood documents Alchemy as its
recommended provider and states that the public RPC is rate-limited and unsuitable for production
throughput: [Robinhood Chain connectivity](https://docs.robinhood.com/chain/connecting/).

## Request authentication

`POST /v1/spot-intents` uses a 32-byte HMAC key shared only with the coordinator. The request must
include:

| Header | Value |
|---|---|
| `X-RTC-Caller` | Exact configured coordinator service identity |
| `X-RTC-Timestamp` | Unix seconds within 30 seconds of writer time |
| `X-RTC-Nonce` | Unique 32–128 character request nonce |
| `X-RTC-Signature` | Lowercase hexadecimal HMAC-SHA-256 |

The signed canonical value is:

```text
METHOD\nPATH\nCALLER\nTIMESTAMP\nNONCE\nSHA256_BODY_HEX
```

The writer verifies the signature in constant time and atomically claims the nonce in PostgreSQL.
A nonce cannot be replayed across writer replicas or process restarts. The API also enforces body,
concurrency, and per-minute admission limits. Render must deploy the signer as a private service;
the Blueprint policy check rejects a public web-service definition.

The coordinator uses the same canonical HMAC protocol for the Lighter signer, with a separate key.
Signer responses are size-bounded and rejected unless the intent identity and transaction hash are
well formed. The coordinator then submits the exact signed `tx_type` and serialized `tx_info` to
Lighter's `/api/v1/sendTx` endpoint with price protection enabled. It never retries an ambiguous
submission blindly, and it requires the API response hash to match the signer response before
recording acceptance. Lighter states that HTTP acceptance does not prove sequencer execution;
order state and fills must be reconciled from authenticated streams before the spot leg is sent:
[Lighter transaction signing](https://apidocs.lighter.xyz/docs/trading),
[sendTx](https://apidocs.lighter.xyz/reference/sendtx).

## Nonce and broadcast protocol

For a new transaction, the writer opens a PostgreSQL transaction, locks the chain-and-signer nonce
row, and selects the greater of the stored and provider-observed pending nonce. It holds that lock
while the typed transaction is simulated, policy-checked, and signed. The signed record and next
nonce commit together.

This ordering establishes the crash boundary:

1. A crash before commit rolls back both the reservation and record.
2. A crash after commit leaves a complete signed record for recovery.
3. Broadcast occurs only after commit.
4. An ambiguous broadcast leaves the exact signed bytes available for idempotent rebroadcast.

No best-effort nonce release exists. Multiple processes serialize on the database row, although the
production topology still requires one writer instance.

## Fee and balance policy

The writer rejects a signature unless all conditions hold:

- estimated gas plus the configured safety margin is below the gas-unit cap;
- priority fee and total fee cap are below absolute wei limits;
- `gas limit × max fee per gas` is below the transaction-cost ceiling;
- the executor balance covers the maximum transaction cost and retains the configured reserve;
- a replacement is within the count and age limits;
- a replacement increases both fee dimensions by at least 12.5 percent when necessary.

These limits are independent of RPC recommendations. A compromised or misconfigured provider
cannot raise the signed fee beyond the local policy. Arbitrum-style L1 data cost is represented by
the provider's gas estimate; the absolute transaction-cost ceiling remains the controlling bound.

## Replacement families

A fee replacement never erases its predecessor. All variants share an intent, nonce, family start
time, and bounded depth. The predecessor becomes `replaced`, but remains in the reconciliation set
until one candidate obtains a canonical receipt. The winner advances through finality states and
all siblings become `superseded`.

This handles the race in which the predecessor is mined while the replacement is being signed or
broadcast. A replaced predecessor is monitored but not rebroadcast. A missing active candidate is
rebroadcast from its exact stored bytes. `nonce too low` is treated as evidence that another family
member may have landed, not as proof that any specific candidate succeeded.

## Reconciliation and finality

Readiness requires a successful verification and a successful pass over the due reconciliation
backlog. A persistent bad row, RPC disagreement, unsupported finality tag, or database fault keeps
readiness latched off. Verification alone cannot restore it.

For each due transaction, the writer:

1. decodes and validates the stored signed transaction and typed payload;
2. queries the primary provider for the receipt;
3. checks the receipt block hash against the canonical block returned by both providers;
4. records successful or reverted execution;
5. resolves the winning nonce variant;
6. advances finality only when both providers have passed the receipt block.

Missing receipts use bounded exponential scheduling so old dropped transactions cannot starve the
newer backlog. A receipt that disappears after confirmation is downgraded to ambiguous and becomes
eligible for exact rebroadcast.

The current mapping is:

| Journal state | Required evidence |
|---|---|
| `soft_confirmed` | Canonical successful receipt from the primary, matching block hash from both providers |
| `l1_posted` | Both providers' `safe` heads are at or beyond the canonical receipt block |
| `ethereum_final` | Both providers' `finalized` heads are at or beyond the canonical receipt block |

Robinhood Chain is an Arbitrum Nitro L2 that posts data to Ethereum and requires L1 execution and
beacon endpoints for a full node: [Robinhood full-node requirements](https://docs.robinhood.com/chain/run-a-full-node/).
Provider and Nitro-version semantics for `safe` and `finalized` must be revalidated during every
release and independent executor review. The service must remain disabled if either provider does
not expose the required semantics.

## Required configuration

Secrets are supplied only through the private deployment environment. They must never appear in
source, examples, build logs, or public documentation output.

| Group | Variables |
|---|---|
| Service | `ROBINHOOD_SIGNER_ENABLED`, `LISTEN_ADDRESS`, `DATABASE_URL` |
| Authentication | `ROBINHOOD_SIGNER_HMAC_KEY`, `SIGNER_CALLER_ID`, admission limits |
| RPC | `ROBINHOOD_RPC_URL`, `ROBINHOOD_RECONCILIATION_RPC_URL`, `ROBINHOOD_CHAIN_ID` |
| KMS | `AWS_KMS_KEY_ID`, `ROBINHOOD_SIGNER_ADDRESS` |
| Governance | timelock, recovery, and guardian addresses |
| Contracts | vault, risk manager, adapter addresses and runtime code hashes |
| Gas policy | gas-unit, priority-fee, fee-cap, transaction-cost, and balance-reserve limits |
| Replacement policy | maximum replacements and maximum family age |

AWS KMS must report `ECC_SECG_P256K1` with `SIGN_VERIFY` usage. The writer requests
`ECDSA_SHA_256` with `MessageType=DIGEST`, decodes DER, validates the scalar range, normalizes
low-S, determines the recovery bit, and requires the recovered address to match the startup key.
AWS documents secp256k1 as the KMS key type intended for cryptocurrency use:
[AWS KMS key specifications](https://docs.aws.amazon.com/kms/latest/developerguide/symm-asymm-choose-key-spec.html).

## Database operations

The migration must be applied before the service starts. Application startup does not create or
alter tables. Required tables hold:

- immutable deployment manifests;
- the chain-and-signer nonce cursor;
- signed transactions and replacement relationships;
- verification evidence and reconciliation schedule;
- expiring request-authentication nonces.

Backups contain signed raw transactions and operational metadata. They are sensitive even though
they do not contain an exportable private key. Access, encryption, retention, PITR, restore tests,
and deletion follow the same controls as the execution database.

Any `quarantined` row is a hard readiness failure. Clearing it requires an incident record,
independent comparison against canonical chain state, and an operator-reviewed migration. Direct
status edits are not a recovery procedure.

## Release verification

The signer release is blocked unless all of the following pass:

- Go formatting, unit tests, race detector, vet, and dependency audit;
- PostgreSQL integration tests on a fresh database;
- restart tests at every sign, persist, and broadcast boundary;
- concurrent nonce and replacement-race tests;
- receipt disappearance, cross-provider disagreement, and finality regression tests;
- hostile fee, low-balance, replacement-limit, and deployment-rotation tests;
- KMS high-S, recovery-bit, malformed-DER, wrong-key, and timeout tests;
- repository identity, secret, and privacy checks;
- private-service Blueprint validation;
- independent signer, key-policy, and incident-response review.

The service remains unfunded and disabled until the wider capital-activation gates pass.
