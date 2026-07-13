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

## Paired-entry saga

Intent admission, venue-event ingestion, and market-authority publication are private,
HMAC-authenticated interfaces with distinct keys and caller identities. Each signature binds the
exact request body, and PostgreSQL claims each request nonce before processing, so replay remains
blocked across coordinator restarts. Lifecycle events cannot be posted through the intent or quote
interfaces. Only the authenticated venue-event path can advance reconciliation.

The coordinator database starts in `HALTED`. Admission requires an operator-reviewed transition to
`ACTIVE`, a latest promotion state of `canary_eligible`, current promotion evidence, and no unresolved
episode anywhere in the deployment. A later `retired` or `rejected` transition revokes admission.
`REDUCE_ONLY` and `HALTED` block new short orders but continue reconciliation, spot hedges, and
emergency unwinds. An ambiguous or failed-safe action atomically returns the coordinator to
`HALTED`.

An admitted intent is current, long-spot/short-perp, multiplier-consistent, and within the absolute
and NAV-relative canary caps. Admission resolves the referenced market manifest from an append-only,
operator-reviewed configuration record and resolves the quote from the separately authenticated
market-data path. Symbol, token, venue market, decimals, contract configuration version, multiplier,
mark, quote block, quote timestamps, entry-price deviation, and minimum spot output must match
reviewed policy. The
coordinator then derives conservative perp notional from the reviewed precision and the greater of
the authoritative mark and executable limit; the declared notional must match. An intent caller
cannot select its own decimal scale or price evidence. Entry and every reserved unwind client-order
index are globally unique for the deployment. Entry and unwind spot intent IDs share a global replay
registry.

Admission records `PrecheckPassed` and enqueues one durable `submit_perp` action. Every claim receives
a random lease-fencing token. All state changes require the token and an unexpired lease, so an old
process cannot complete or rebroadcast an action after a replacement worker has reclaimed it. Action
keys prevent duplicate stages for the same intent. A `submit_perp` claim also captures the current
execution-control version. Immediately before the first irreversible venue send, the worker locks
the action and control record in one transaction and records authorization only if the mode remains
`ACTIVE` at the claimed version. A halt increments the version, fencing every older claim. A
persisted signature alone is not proof of a send and cannot cross an expired or halted authorization
boundary. A signature paired with durable send authorization, or any recorded submission attempt, is
treated as potentially live; it is reconciled after restart and cannot be converted to an expired
no-exposure result.

The entry path is:

1. fetch Lighter's next nonce for the configured account and API-key index;
2. reserve the greater of the observed and journaled nonce in the same transaction that binds it to
   the action;
3. request an IOC short signature, then persist the exact signed response;
4. submit through `sendTx`, persist the accepted response, and reconcile any timeout, rejection, or
   response-hash mismatch as an unknown outcome;
5. wait for authenticated account-stream acceptance, rejection, partial-fill, and terminal-fill
   events;
6. scale settlement input, minimum output, and target Stock Token quantity to the terminal perp fill;
7. submit the typed spot intent through the KMS writer and wait for canonical chain observation;
8. enter `Hedged` only when the multiplier-adjusted spot fill exactly matches the perp exposure.

A signer timeout before Lighter submission is retryable because signing has no venue side effect.
After any `sendTx` attempt, the reserved nonce is never released. HTTP failures and explicit API
rejections cannot prove that the sequencer did not accept an earlier identical submission, so the
coordinator halts new entries and retains a reconciliation action keyed by transaction hash and
client-order index.

Authenticated observations carry stable native event identity, publisher and receiver timestamps,
source sequence, order or intent identity, transaction hash, and authoritative cumulative fill.
Identity is the source, source session, and native event ID tuple because venue sequences may reset
after reconnect. Duplicates within that tuple must match byte-for-byte normalized evidence; conflicts
halt the coordinator. Sequence continuity is enforced within each source session. A gap is
quarantined, but the append-only raw stream can heal when the missing sequence arrives: the locked
session frontier advances across every now-contiguous stored event and revalidates its action binding
before eligibility. Late events behind the frontier remain quarantined. Lighter reconciliation binds
the transaction hash, client-order index, market, and direction. Robinhood reconciliation binds the
single-use typed intent ID and configuration version. A fee replacement can confirm under a different
transaction hash, so a predecessor hash identifies family evidence rather than the spot intent.
Unrelated late events are retained as warning evidence without failing the active recovery action.

Historical observations remain admissible after an outage. Request authentication is current, while
publisher and receiver timestamps retain the original venue or chain times and may predate ingestion.
Future-dated evidence is rejected. The database creation timestamp records when the coordinator
ingested the backfill.

Robinhood submissions are idempotent by request ID and body digest in the writer journal. The
coordinator persists the exact typed request before the call, reconstructs it from immutable intent
data after restart, and rejects any mismatch before retrying. `signed`, `submitted`,
`soft_confirmed`, `l1_posted`, `ethereum_final`, `ambiguous`, `replaced`, `superseded`, and
`quarantined` all require chain reconciliation; none proves that the typed intent had no chain
effect. Only an Ethereum-final reverted receipt proves that candidate did not execute. HTTP 409 is
also ambiguous because it can refer to an earlier journal row under the same request ID, so it halts
admission and creates a reconciliation action instead of triggering a perp unwind. Unknown statuses
fail closed. Transaction submission deadlines and reconciliation deadlines are separate: an expired
intent blocks a new transaction but does not stop recovery of an already journaled transaction or
finality tracking. An overdue journaled or submitted transaction halts admission and leaves a durable
recovery action; an unknown capital state is never terminalized merely because a wall-clock deadline
passed.

A hedged episode does not close implicitly. The strategy or operator submits a separately
HMAC-authenticated `POST /v1/exits` request with one of the reviewed exit reasons and an exact
reference to a fresh, append-only execution-authority quote. An operator may use the same path to
close a terminal known perp fill before any spot acquisition; that authority binds zero spot input
and zero expected spot output. The record binds the intent, reviewed
market manifest, Lighter mark, exact spot inventory, expected settlement output, publisher and
receiver times, quote block, and expiry. The request supplies the worst acceptable reduce-only buy
price, minimum settlement output, submission deadline, and independent reconciliation deadline.
The coordinator enforces direction-aware price deviation and spot slippage against the reviewed
market configuration, then revalidates the same quote immediately before each signer send.

The coordinator records `ExitStarted`, enters reduce-only unwind state, closes the actual perp fill
through the bounded repair path, sells the exact recorded spot inventory, and requires
Ethereum-final balance conservation before emitting `Closed`. Emergency actions cannot reuse the
entry-time price or minimum output. They wait for the newest matching execution-authority quote;
missing or expired authority halts the send. A fresh authorized exit can resume an `Unhedged` or
incomplete `Unwinding` episode without releasing the global exposure lock. Automatic repair remains
bounded by the intent. After that bound, only an authenticated operator exit can allocate another
reduce-only order; its client-order index comes from a durable recovery sequence and is registered in
the global identifier ledger before signing. Exit actions remain permitted while entry admission is
halted.

Spot rejection, deadline expiry after a known perp fill, or a mismatched spot fill schedules a
reduce-only IOC perp unwind. Partial unwind fills update cumulative open exposure and schedule a
bounded repair order with a separately reserved client-order index. If spot inventory exists, the
coordinator then submits a separate typed sell intent with its own replay-protected contract intent
ID and verifies input and output balance conservation at Ethereum finality. The saga closes only
after every acquired leg is proven closed. `Unhedged` retains the global exposure lock and accepts
only an authorized recovery unwind. A separately authenticated `POST /v1/recoveries` request cannot
create a blind send: it derives a successor only from a failed or ambiguous action's persisted
request, send authorization, transaction identity, and payload. It resumes reconciliation after a
poisoned post-broadcast action or replays the exact Robinhood request when its response was lost. The
action enters failed-safe status and halts admission, while the saga remains in its recoverable
capital state. The legacy `FailedSafe` saga state remains terminal and retains the lock. Neither path
can authorize a new episode. No worker action can invoke
withdrawal, transfer, governance, arbitrary calldata, leverage changes, or an untyped route.

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
- append-only reviewed market configurations and authenticated executable-price evidence;
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
