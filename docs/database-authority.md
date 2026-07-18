# Database authority

Production services never receive a schema-owner connection string at runtime.
Render injects owner bindings into the pre-deploy and launcher environments.
Pre-deploy applies the reviewed migration manifest and provisions one login role.
The launcher then uses `scripts/database-runtime-exec.rb` to derive the restricted
connection URL and removes the owner URL and role password before executing the
service. The release bootstrap's bounded one-off evidence jobs use the same
bindings only to derive read-only URLs while every production service remains
suspended.

Runtime migrations are disabled in production:

- `APP_RUN_MIGRATIONS=false`
- `RUNTIME_RUN_MIGRATIONS=false`
- `LIGHTER_PROVISIONER_RUN_MIGRATIONS=false`
- `ROBINHOOD_PROVISIONER_RUN_MIGRATIONS=false`
- `SEQUENCER_RUN_MIGRATIONS=false`
- `AAPL_RELAY_RUN_MIGRATIONS=false`

## Roles

| Database | Runtime role | Authority |
|---|---|---|
| Product | `robin_app_api` | Product API tables |
| Product | `robin_app_paper` | Agent reads and paper-event writes |
| Product | `robin_app_readonly` | Product reads |
| Research | `robin_research_collector` | Capture, health, staging, and archive tables |
| Research | `robin_research_paper` | Paper evaluation and fanout tables |
| Research | `robin_research_readonly` | Research reads |
| Execution | `robin_execution_coordinator` | Execution journals and controls |
| Execution | `robin_execution_live_control` | Live scheduler journals and reviewed market configuration |
| Execution | `robin_execution_sequencer_1` through `_3` | One sequencer journal identity per publisher |
| Execution | `robin_execution_aapl_relay_1` through `_3` | One AAPL relay journal identity per publisher |
| Execution | `robin_execution_readonly` | Execution reads |
| Lighter | `robin_lighter_provisioner` | Encrypted credential and nonce journals |
| Lighter | `robin_lighter_readonly` | Non-secret signing-claim receipt reads |
| Custody | `robin_custody_provisioner` | Canonical binding and provisioner audit tables |
| Custody | `robin_custody_signer` | Robinhood signer journal |
| Custody | `robin_custody_readonly` | Custody reads |

Every role is `NOSUPERUSER`, `NOCREATEDB`, `NOCREATEROLE`, `NOINHERIT`,
`NOREPLICATION`, and `NOBYPASSRLS`. Roles receive no database-owner membership,
schema or temporary-table creation authority, migration-ledger access, or blanket function
execution. Function grants are derived from constraints and triggers on writable
tables. The research collector additionally receives the exact
`ensure_event_staging_partition(timestamptz)` function, which is a schema-pinned
security-definer function used only to create monthly staging partitions.
Provisioning fails before making changes when the database owner lacks
`CREATEROLE`.

Delete authority is limited to expiring product, coordinator, provisioner, and
signer nonce rows plus archived research staging rows. Publisher journals,
credential records, custody bindings, audit records, and execution journals
cannot be deleted by runtime roles.

The six on-chain publishers have distinct passwords and login roles. Forced
row-level policies bind each role to its pinned `publisher_id` for reads and
writes, while the read-only evidence role may observe all journals. A publisher
cannot select, insert, update, or delete another publisher's state or
transactions. Deprecated shared publisher roles are disabled and stripped when
the replacement roles are provisioned.

## Migration release

Migration manifests are ordered and checksum-pinned. Execution migrations use a
session advisory lock and one transaction for the complete coordinator,
scheduler, evaluation, sequencer, and relay chain. Before that transaction
starts, an existing control plane is committed to `HALTED`. A checksum or
migration failure therefore cannot restore an earlier `ACTIVE` state.

The final execution transaction rejects release when it finds:

- an active execution episode;
- pending, leased, or ambiguous execution work;
- created or ambiguous signer requests;
- a registered account without fresh Lighter and Robinhood snapshots;
- a non-flat or nonce-misaligned snapshot;
- a snapshot whose account, owner, vault, or signer differs from the immutable registration;
- unknown Lighter orders or positions; or
- unhealthy Robinhood finality.

A successful migration still leaves global, strategy, and account controls
halted. Activation remains an explicit operator action after runtime readiness
has been observed. Account controls already carrying the strategy-release
recovery reason retain that reason so the constrained close-and-reprovision path
remains available.

## Verification

`scripts/database-authority.integration.test.sh` migrates five fresh PostgreSQL
databases and exercises every runtime role. It performs real product, collector,
paper, coordinator, live-control, publisher, provisioner, and signer operations;
checks trigger and constraint functions; proves cross-role writes fail; and
tests active, missing, stale, unknown, non-flat, and reconciled quiescence states.
CI runs the product database on PostgreSQL 16, research on PostgreSQL 17, and
execution, Lighter, and custody on PostgreSQL 18, matching `render.yaml`.
