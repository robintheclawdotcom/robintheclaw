# Promotion evidence ledger

This private operator CLI records promotion evidence for the fixed `basis-aapl-v1` release. It does
not enable execution, change capital limits, or expose a user API. Repository activation remains
disabled independently by `config/mainnet-live-policy.json` and the service enable flags.

The ledger permits only:

`paper -> shadow -> canary -> cohort -> public`

Any non-retired stage may instead move to `retired`. Retirement is terminal. Every entry pins the
canonical strategy manifest, route, oracle policy, risk policy, code commit, and an operator-supplied
SHA-256 of the built code artifact. A different digest requires a new ledger; it cannot mutate the
identity of an existing release.

High and critical incidents are always stage-failing and set the clean-observation start to `null`.
Closing the incident does not restart the clock. The operator must record a separate
`start-observation` entry after every stage-failing incident is closed. Warnings are retained without
resetting the clock.

## Trust boundary

Every JSONL entry is domain-separated, SHA-256 hash-chained, and signed by one trusted Ed25519
release key. The private key is read from a mode-0600 file and is never written to the ledger or
checkpoint. The trusted public key is an operational input and must be distributed independently of
the ledger.

The mode-0600 checkpoint stores the last trusted sequence and entry hash. Keep it in an independent,
durable operator system or write-once evidence store. Before every append, the CLI verifies the
complete ledger and every signature, then requires the ledger to contain the checkpointed hash at
the checkpointed sequence. This rejects a valid-prefix truncation, rollback, or fork. A checkpoint
stored only beside the ledger protects against accidental rollback but is not an independent trust
anchor.

Mutation commands take an exclusive directory lock, append and `fsync` one complete record, then
atomically replace and `fsync` the checkpoint. A crash after the ledger append but before checkpoint
replacement leaves a valid ledger that extends the old checkpoint; verify it before retrying the
operator decision. A stale `.lock` directory requires manual investigation and must not be removed
while another process could be running.

## Commands

All mutations require these arguments:

```text
--ledger /private/path/promotion.jsonl
--checkpoint /independent/path/promotion.checkpoint.json
--private-key /private/path/promotion-ed25519.pem
--public-key /trusted/path/promotion-ed25519.pub.pem
--code-sha256 <64 lowercase hex characters>
--evidence-sha256 <64 lowercase hex characters>
```

Initialize the fixed strategy at `paper`:

```sh
node ops/mainnet-live/promotion-ledger/cli.mjs init <arguments>
```

Record forward promotion or terminal retirement:

```sh
node ops/mainnet-live/promotion-ledger/cli.mjs promote <arguments> --to shadow
node ops/mainnet-live/promotion-ledger/cli.mjs retire <arguments>
```

Record and close incidents, then explicitly restart the observation clock:

```sh
node ops/mainnet-live/promotion-ledger/cli.mjs incident <arguments> --incident-id INC-0001 --severity high
node ops/mainnet-live/promotion-ledger/cli.mjs close-incident <arguments> --incident-id INC-0001
node ops/mainnet-live/promotion-ledger/cli.mjs start-observation <arguments>
```

`verify` and `status` require the ledger, checkpoint, public key, and code digest, but no private
key or evidence digest:

```sh
node ops/mainnet-live/promotion-ledger/cli.mjs verify \
  --ledger /private/path/promotion.jsonl \
  --checkpoint /independent/path/promotion.checkpoint.json \
  --public-key /trusted/path/promotion-ed25519.pub.pem \
  --code-sha256 <64 lowercase hex characters>
```

The status output always includes `"executionEnabled":false`. Promotion evidence is necessary but
never sufficient authorization to enable capital.
