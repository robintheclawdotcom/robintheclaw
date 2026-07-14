# Infrastructure readiness

`render.yaml` is the production topology definition. The coordinator, publishers, scheduler,
evaluation authority, provisioners, and both signers are enabled. Live admission is account-scoped
and remains impossible until every authenticated readiness input is current.

## Service boundaries

| Service | Render type | Network exposure | Authority |
| --- | --- | --- | --- |
| `robintheclaw` | Web | Public custom domain | Public documentation and authenticated product interface |
| `robin-api` | Private service | Render private network | Authenticated product data and personal-vault preparation |
| `robin-research-collector` | Worker | Outbound only | Venue and chain reads, Postgres staging, R2 archive writes |
| `robin-paper-agent` | Worker | Outbound only | Executable quotes, matched paper positions, P&L, and opportunity episodes |
| `robin-execution-coordinator` | Private service | Render private network | Durable intent admission and lifecycle coordination |
| `robin-lighter-signer` | Private service | Render private network | Restricted Lighter order and cancellation signatures |
| `robin-robinhood-signer` | Private service | Render private network | Typed `executeSpot` transactions through one KMS key |
| `robin-research` | Render Postgres | Private network only | Runtime, evidence, saga, nonce, and audit state |

No execution endpoint is public. The product API receives no signer credential. Restriction actions
use the signed `restrictctl` operator path, while activation is derived from the committed canary
policy and current account evidence. Render service references provide the coordinator with private
signer host and port values. The coordinator constructs private HTTP endpoints only from validated
single-label service names and numeric ports; arbitrary public plaintext signer URLs are rejected.

## Deployment state and readiness

The execution services enter the Blueprint with these values:

```text
COORDINATOR_ENABLED=true
LIGHTER_SIGNER_ENABLED=true
ROBINHOOD_SIGNER_ENABLED=true
```

`/livez` proves only that the process is running. `/readyz` requires the service's database and
private dependencies. Account readiness is stricter and is published separately from process
health.

Deploying a release requires:

1. a reviewed release commit with all required checks passing;
2. applied and verified database migrations;
3. a production RPC pair, R2 credentials, KMS key, and venue subaccount scoped to the service;
4. an approved deployment manifest and code hashes;
5. a successful backup restore and incident-response review;
6. the internal audit and key review recorded for the exact release;
7. an operator record identifying the release, configuration digest, and rollback owner.

Running a signer or coordinator does not authorize an intent without `canary_eligible` evidence and
current account readiness. Shared integrity failures restrict global admission; tenant failures
restrict only the affected account.

## Database connections

Both databases have no external IP allowlist and use Pro instances, high-availability standbys, and
storage autoscaling. The research database also uses integrated PgBouncer for runtime services.

- Collector, paper agent, coordinator, and Robinhood signer use the pooled internal connection string.
- Collector and paper agent use a separate direct private connection while applying runtime migrations.
- The product API uses the direct internal application database connection. Its migration authority
  must move to the release identity before activation.
- Migrations are release tasks. Signers and the coordinator do not acquire schema authority at
  startup.
- The release job owns runtime migrations. Execution services never acquire schema authority at
  startup.

CI applies every runtime migration to a disposable PostgreSQL 18 instance and runs the coordinator's
ignored database promotion test explicitly. The Robinhood signer journal test runs against the same
class of disposable database in the Go job and cannot silently skip there.

## Secret and configuration policy

The Blueprint contains no credentials, private keys, operational addresses, code hashes, balances,
or live risk limits. Values in those classes use `sync: false`; generated API tokens use Render's
secret generator. Production values are entered in the Render environment after the service exists.

Credential separation is mandatory:

- R2 uses a bucket-scoped object read/write credential, not the Cloudflare management token.
- The Lighter signer receives only its dedicated capped-subaccount API key.
- The Robinhood signer receives one non-exportable KMS key and no Lighter credential.
- The coordinator receives no private key.
- The product API receives no signer credential.
- The public web service receives no private runtime credential.

Signer requests use a distinct 32-byte HMAC key per signer. The signature binds the method, path,
caller, timestamp, request nonce, and exact request-body digest. Both signers reject timestamps
outside a 30-second window and reject nonce replay. The Lighter signer's replay cache covers the
authorization window in memory; the coordinator remains responsible for durable intent and venue
nonce journals across restarts. Signer concurrency and per-minute request rates are bounded before
private-key operations begin.

Coordinator writers are separated again by scope: shadow intent admission, operator exit and
recovery, runtime venue events, and execution-authority quotes use distinct callers and HMAC keys.
Their nonces are claimed durably before a request reaches the store. Recovery shares the operator
exit credential, cannot be called by the shadow or collector services, and can enqueue only a
successor derived from an ambiguous or failed-safe durable action record.

Environment changes are treated as deployment changes. After a change, readiness remains off until
the service has revalidated its dependency identities and persisted configuration digest.

## Build and release controls

Every service builds from the repository root without a Blueprint `rootDir`. Rust builds use locked
Cargo resolution, Node uses `npm ci`, and Go verifies its module cache before compiling. Both signer
modules, CI, and Render pin the same security-patched Go toolchain. Automatic deployment is
`checksPass` for every service.

The release sequence is:

1. validate repository policy, Blueprint policy, and the current official Render JSON Schema;
2. run Foundry, Rust, Go, Node, migration, security, dependency, leak, and identity checks;
3. merge the reviewed release through protected `main`;
4. sync the Blueprint and review the resource diff;
5. deploy the service revisions and verify `/livez`;
6. apply migrations as a separate release action;
7. populate approved secrets and configuration;
8. verify `/readyz` for every private dependency;
9. record canary account readiness evidence;
10. restrict admission immediately if any identity, reconciliation, or health check disagrees.

## Per-account admission

The mainnet services and canary policy are enabled. An execution account becomes `ACTIVE` when the
control plane verifies all of the following from current evidence:

- the account binding, strategy version, signer identities, vault graph, and runtime code hashes are canonical;
- the user's vault and Lighter subaccount are funded within policy, and both execution signers have gas;
- authenticated Lighter orders, positions, collateral, trades, nonce state, and stream continuity are fresh;
- dual-RPC Robinhood receipts, balances, vault wiring, and finality observations agree;
- executable perp and block-pinned spot quotes are less than five seconds old;
- margin coverage is at least twice maintenance, nonces are aligned, and there are no unknown orders or positions;
- the route, oracle, and sequencer quorum are healthy and current;
- global, strategy, and account controls permit entry, with no unresolved reconciliation ambiguity;
- the exact release has no open internal critical or high contract, executor, or key finding.

A shared integrity failure restricts global admission; an account-specific failure restricts only
that account and leaves reduce-only recovery available.
