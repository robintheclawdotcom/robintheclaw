# Infrastructure readiness

`render.yaml` is the production topology definition. Declaring a service does not authorize it to
trade. The coordinator and both signers are created disabled, use liveness-only deployment checks,
and remain unable to pass readiness until their separate activation requirements are satisfied.

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

No execution endpoint is public. The product API is not the operator control plane and receives no
signer credential. A separate authenticated, read-only operator service remains a technical
readiness blocker. Render service references provide the coordinator with private signer host and
port values. The coordinator constructs private HTTP endpoints only from validated single-label
service names and numeric ports; arbitrary public plaintext signer URLs are rejected.

## Deployment state and readiness

The execution services enter the Blueprint with these values:

```text
COORDINATOR_ENABLED=false
LIGHTER_SIGNER_ENABLED=false
ROBINHOOD_SIGNER_ENABLED=false
```

`/livez` proves only that the process is running. Disabled services return an unsuccessful response
from `/readyz` and reject every write request. Render deploys use `/livez` because an intentionally
disabled service must be deployable without being represented as operationally ready.

Changing an enable flag is a release operation, not routine configuration. It requires:

1. a reviewed release commit with all required checks passing;
2. applied and verified database migrations;
3. a production RPC pair, R2 credentials, KMS key, and venue subaccount scoped to the service;
4. an approved deployment manifest and code hashes;
5. a successful backup restore and incident-response review;
6. the audit, legal, venue, key, and empirical promotion evidence required by the execution gate;
7. an operator record identifying the approver, release, configuration digest, and rollback owner.

Enabling one signer does not enable the coordinator. Enabling the coordinator does not authorize an
intent without `canary_eligible` evidence. The durable coordinator mode is independently initialized
to `HALTED`; changing an environment flag does not change it. Contract deployment does not authorize
funding.

## Database connections

Both databases have no external IP allowlist and use Pro instances, high-availability standbys, and
storage autoscaling. The research database also uses integrated PgBouncer for runtime services.

- Collector, paper agent, coordinator, and Robinhood signer use the pooled internal connection string.
- Collector and paper agent use a separate direct private connection while applying runtime migrations.
- The product API uses the direct internal application database connection. Its migration authority
  must move to the release identity before activation.
- Migrations are release tasks. Signers and the coordinator do not acquire schema authority at
  startup.
- The collector may run runtime migrations only until migration ownership is moved into the release
  job. Production activation remains blocked until that transition is complete.

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
5. deploy disabled services and verify `/livez`;
6. apply migrations as a separate release action;
7. verify `/readyz` remains unsuccessful before activation;
8. populate approved secrets and configuration;
9. enable one service at a time in dependency order and record its readiness evidence;
10. revert enable flags immediately if any identity, reconciliation, or health check disagrees.

## Remaining infrastructure blockers

The Blueprint is an infrastructure boundary, not evidence that the full runtime is ready. Capital
activation remains blocked while any of the following is true:

- collector production RPC, sequencer verification, archive reconciliation, or soak evidence is incomplete;
- runtime collectors do not yet deliver authenticated Lighter account and Robinhood chain
  observations into the coordinator's durable reconciliation stream;
- the execution-authority publisher does not yet deliver contemporaneous Lighter marks and
  block-pinned Uniswap v4 exact-input spot-exit quotes, including reviewed pool, hook, and runtime
  code-hash evidence;
- the coordinator does not yet verify live Lighter collateral, margin, and account position before
  signing, so the declared leverage field is not a capital-safety control;
- either signer relies on an authentication or nonce design with an unresolved audit finding;
- R2 retention locks, database recovery exports, telemetry sinks, or alert routes are unverified;
- contract, executor, key, legal, venue, or empirical promotion evidence is incomplete.

These blockers must be resolved in code and release evidence. They are not waived by a successful
Render deploy.
