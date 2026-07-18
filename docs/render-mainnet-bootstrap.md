# Render mainnet bootstrap

`render.yaml` manages one protected, network-isolated Render environment:
`Robin the Claw / Production`. It contains 20 services, five Postgres databases, and 23
Blueprint-generated secret groups. Every service has auto-deploy disabled. The public web service
is deployed last by the release script.

The release is fail-closed around a database-derived quiescence receipt. Static repository
validation is necessary but does not establish live readiness.

## Preconditions

Keep every local key, database URL, AWS output, and receipt outside Git with mode `0600`.

```sh
export RENDER_WORKSPACE_ID="$(jq -r .workspace .renderctl.json)"
export RENDER_TOKEN_FILE="keys/render-api-token"

ruby scripts/validate-blueprint.rb
ruby scripts/validate-blueprint.test.rb
ruby scripts/validate-aws-bootstrap.rb
ruby scripts/validate-aws-bootstrap.test.rb
ruby scripts/render-mainnet-bootstrap.test.rb
node --test scripts/provision-render-env-groups.test.mjs
```

Provision the authentication groups before the first Blueprint sync. Config mode updates reviewed
values, adds missing keys, removes unreviewed keys, and verifies the result without printing
secrets.

```sh
node scripts/provision-render-env-groups.mjs auth \
  --owner "$RENDER_WORKSPACE_ID" \
  --token-file "$RENDER_TOKEN_FILE"

node scripts/provision-render-env-groups.mjs config \
  --owner "$RENDER_WORKSPACE_ID" \
  --token-file "$RENDER_TOKEN_FILE" \
  --config keys/render-mainnet-env-groups.json
```

## Phase 1: prepare

Inspect is read-only:

```sh
ruby scripts/render-mainnet-bootstrap.rb inspect \
  --workspace "$RENDER_WORKSPACE_ID" \
  --token-file "$RENDER_TOKEN_FILE"
```

Prepare first disables auto-deploy on the existing public web service and confirms that it remains
running on `main` from `https://github.com/robintheclawdotcom/robintheclaw`. It then creates any
missing controlled service as a no-op shell, disables its
auto-deploy, suspends all 19 controlled services, waits for confirmed suspension, and emits the
service IDs needed by the AWS OIDC stack. A failed prepare re-discovers the workspace and
best-effort suspends every controlled service it can identify; it does not suspend the public web
service.

```sh
umask 077
ruby scripts/render-mainnet-bootstrap.rb prepare \
  --workspace "$RENDER_WORKSPACE_ID" \
  --token-file "$RENDER_TOKEN_FILE" \
  --confirm PREPARE \
  > keys/render-prepare-receipt.json
```

The confirmation is intentionally phase-specific. Do not make it a CI default.
Every later mutating phase requires this mode-`0600` receipt and rejects any service whose current
ID differs from the prepared ID.

Deploy `ops/aws/render-kms-bootstrap.yaml` with the exact workspace, environment, and three
AWS-bound service IDs from the prepare receipt. Save the unmodified
`aws cloudformation describe-stacks` output outside Git.

## Phase 2: adopt and bind

Sync the reviewed Blueprint in the existing workspace. Inspect the plan before applying it. Render
must adopt resources with matching names, keep every service on `main` with auto-deploy off, and
leave the 19 controlled services suspended.

The Blueprint gives owner database URLs only to pre-deploy migration commands. Each start command
derives a least-privilege runtime URL, removes the owner URL and role password from the environment,
and then execs the service. A second Blueprint managing any of these resources is forbidden.

Bind CloudFormation outputs with per-key environment updates:

```sh
ruby scripts/render-mainnet-bootstrap.rb bind \
  --workspace "$RENDER_WORKSPACE_ID" \
  --token-file "$RENDER_TOKEN_FILE" \
  --prepare-receipt keys/render-prepare-receipt.json \
  --aws-outputs keys/render-mainnet-aws-outputs.json \
  --confirm BIND
```

A mode-`0600` service file must pin every reviewed `sync: false` value:

```json
{
  "services": {
    "robin-account-publisher": {
      "ACCOUNT_PUBLISHER_PRIMARY_RPC_URL": "<set locally>"
    }
  }
}
```

Pass it to both bind and activate with `--service-env keys/render-mainnet-service-env.json`.
Unknown services, missing or undeclared keys, empty values, conflicting AWS outputs, and static AWS
credentials are rejected. Activation compares every live direct value exactly. It also compares
the seven reviewed configuration groups against `keys/render-mainnet-env-groups.json`; random
authentication groups must have their exact canonical keys and 32-byte lowercase-hex values.

## Phase 3: initialize databases and prove quiescence

Before collecting evidence, use the operator control plane to set global, strategy, and account
controls to `HALTED`. Reconcile every registered account flat, terminalize commands and scheduler
work, drain outboxes, and resolve every signer or send ambiguity. Keep all controlled Render
services suspended. Create the signing key once:

```sh
umask 077
openssl rand -hex 32 > keys/render-quiescence-key
```

```sh
export RELEASE_COMMIT="$(git rev-parse HEAD)"
export RENDER_ENVIRONMENT_ID="<evm-... from prepare>"

ruby scripts/render-mainnet-bootstrap.rb initialize-databases \
  --workspace "$RENDER_WORKSPACE_ID" \
  --token-file "$RENDER_TOKEN_FILE" \
  --prepare-receipt keys/render-prepare-receipt.json \
  --commit "$RELEASE_COMMIT" \
  --quiescence-key-file keys/render-quiescence-key \
  --evidence-output keys/render-quiescence-evidence.json \
  --output keys/render-quiescence-receipt.json \
  --confirm INITIALIZE-DATABASES
```

Initialization suspends the public web service and keeps it suspended through activation. It
temporarily sets the start command to `sleep infinity` on exactly three migration owners, pins
their reviewed pre-deploy commands, deploys the exact release, waits for migrations, suspends them,
and restores their reviewed start commands:

- `robin-live-control` initializes app, research, and execution schemas;
- `robin-account-publisher` initializes execution and custody schemas;
- `robin-lighter-provisioner` initializes the Lighter schema.

This creates the four read-only receipt roles without starting any product, signer, publisher, or
execution process. It then launches four concurrent one-off evidence jobs inside the isolated
Render environment. Before any job is created, the collector requires each base service to belong
to the receipt's exact environment, deploy from `main` with auto-deploy disabled, and have the
receipt's exact commit as its latest live deploy. A suspended evidence-base service is temporarily
configured to run only `sleep infinity` and confirmed resumed before job creation; a base service
that is already running remains untouched. After the jobs finish, every temporary base is
re-suspended and its reviewed command is restored. The job runner independently rejects a
suspended base, so receipt collection does not rely on undocumented job creation behavior for
suspended services. Each job derives an in-memory role-specific URL, forces a read-only
transaction, emits one nonce-bound evidence envelope, and exits. Database URLs and credentials
never enter local arguments, output, or logs.

For a later release whose schemas already exist, collect a new receipt directly:

```sh
ruby scripts/render-mainnet-bootstrap.rb collect-receipt \
  --workspace "$RENDER_WORKSPACE_ID" \
  --environment "$RENDER_ENVIRONMENT_ID" \
  --token-file "$RENDER_TOKEN_FILE" \
  --prepare-receipt keys/render-prepare-receipt.json \
  --commit "$RELEASE_COMMIT" \
  --quiescence-key-file keys/render-quiescence-key \
  --evidence-output keys/render-quiescence-evidence.json \
  --output keys/render-quiescence-receipt.json \
  --confirm COLLECT-RECEIPT
```

Caller-supplied counters are not accepted. The receipt is valid for five minutes and is bound to
the workspace, environment, release commit, and SHA-256 digest of the evidence.

Collection fails unless:

- global, strategy, and account controls are `HALTED`;
- no non-closed execution account has an immutable registration;
- no episode or account is non-flat, with both venue snapshots unexpired and at most five seconds
  old;
- no execution action is pending or leased;
- no product or execution command, scheduler item, or app outbox item is in flight;
- no Lighter signing claim, signed-unsent Robinhood transaction, pending custody transaction, or
  unresolved ambiguity exists.

The registered-account restriction is deliberate. The current activation sequence suspends every
controlled service while staging exact release artifacts, so the account publisher cannot refresh
five-second venue snapshots during a long build. Widening that freshness window would make stale
state look authoritative. Until a release-only reconciler can query Lighter with signer-private
credentials, query Robinhood through both reviewed RPCs, and attest the exact registered account
set without enabling execution, receipt collection and activation fail before any service is
staged. A subsequent release with a registered account must not use this bootstrap.

## Phase 4: activate

Run activation immediately after receipt collection:

```sh
ruby scripts/render-mainnet-bootstrap.rb activate \
  --workspace "$RENDER_WORKSPACE_ID" \
  --token-file "$RENDER_TOKEN_FILE" \
  --prepare-receipt keys/render-prepare-receipt.json \
  --aws-outputs keys/render-mainnet-aws-outputs.json \
  --service-env keys/render-mainnet-service-env.json \
  --env-group-config keys/render-mainnet-env-groups.json \
  --commit "$RELEASE_COMMIT" \
  --quiescence-key-file keys/render-quiescence-key \
  --quiescence-receipt keys/render-quiescence-receipt.json \
  --confirm ACTIVATE
```

Activation verifies the exact workspace, protected and network-isolated environment, Blueprint
shape, database topology and external isolation, generated groups, direct secrets, AWS bindings,
disabled auto-deploy, the public web health path, and the signed receipt. Activation re-runs the
four internal evidence jobs before staging. The initial receipt is not trusted as a current state
snapshot after builds begin.

Render returns HTTP 409 when asked to deploy a suspended service. The script therefore stages the
release one service at a time:

1. pin the reviewed pre-deploy command and replace the start command with `sleep infinity`;
2. temporarily resume only that service;
3. deploy the exact 40-character Git commit and wait for Render's `live` state;
4. suspend it and restore the reviewed start command.

No prior or target application process runs during staging. Every pre-deploy migration runs in
this isolated sequence. Execution migrations take an advisory lock, preserve restrictive controls,
and reject non-terminal execution or signer work. After all artifacts are staged, the script
confirms that every controlled service is suspended and collects fresh internal evidence again.

Final startup re-collects quiescence evidence before every batch, then resumes and verifies:

1. research, paper, sequencer, AAPL relay, and provisioner services;
2. both signers;
3. the execution coordinator;
4. the product API;
5. the account publisher and quote authority;
6. the exit quote publisher and strategy runner;
7. live control.

The exit quote publisher never starts in the same batch as its quote-authority dependency.
Live control never starts in the same batch as its strategy-runner dependency. The public web
service is resumed and deployed to the exact commit only after every backend batch is live on that
commit. The Lighter provisioner starts before the product API, preserving the rolling compatibility
boundary where the new API requires authenticated provisioner responses.

Render supports HTTP health-check paths only for web services. For every backend batch, activation
therefore requires two runtime signals after resume:

- every service must expose exactly one fresh Render runtime instance created by that resume, and
  the same instance ID must remain present for a 15-second stability window;
- every private HTTP service in the batch must pass a nonce-bound internal probe from a one-off job
  using the exact `robin-live-control` release artifact and Render private-network service
  references.

When `robin-live-control` is still suspended, activation applies the same no-op-base guard used for
receipt jobs: it temporarily resumes `sleep infinity`, creates and finishes the probe job only
after confirmed resume, then re-suspends and restores the reviewed command.

The internal probe requires `RENDER_GIT_COMMIT` to equal the release commit. It calls `/readyz` on
the product API, coordinator, account publisher, provisioners, and signers, and `/health` on the
quote authority and strategy runner. Product API readiness requires Robinhood and product RPC
connectivity, database connectivity, authentication, provisioner and coordinator configuration,
and the pinned mainnet strategy. Account-publisher readiness requires a fresh successful discovery
cycle; an empty but valid account set is process-ready, while any rejected binding is not. The
endpoint response must be HTTP 200 with the service's exact reviewed status. The job emits one release-, service-, and
nonce-bound evidence envelope; missing, duplicate, or mismatched evidence fails activation.

Background workers cannot receive private-network probes on Render. Their current process is
verified by the fresh, stable runtime-instance observation. This proves the resumed process
remained running through the stability window; venue cycles and account state remain separate
fail-closed admission evidence and are never inferred from process liveness.

Any error triggers best-effort suspension of all 19 controlled services and, once the release has
quiesced it, the public web service. Nonterminal deploys are canceled and allowed to settle before
cleanup is reported. After suspension, a new read-only database observation must confirm that the
global, strategy, and account controls are still `HALTED`. If either service suspension or this
control observation cannot be confirmed, the command says exactly which rollback evidence is
missing and does not report a successful rollback. The deploy wait covers Render's documented
build, pre-deploy, and startup windows. Activation never changes a control to `ACTIVE`.
The output contains deploy IDs, startup batches, every fresh evidence digest, and per-batch runtime
instance/probe evidence, but no secret or database URL.

Activation does not fund an account, authorize an execution account, or change a kill switch.
Those remain explicit post-deploy operations.

## Scope and recurring cost

| Resource | Quantity | Monthly subtotal |
|---|---:|---:|
| Starter services | 18 | $126 |
| Standard services | 2 | $50 |
| Pro-4gb Postgres compute, including five HA standbys | 10 | $550 |
| Postgres storage, including HA standby storage | 1,000 GB | $300 |
| Pro workspace | 1 | $25 |
| Baseline |  | **$1,051/month** |

The total excludes AWS KMS, Lambda, DynamoDB, RPC providers, outbound bandwidth, excess build
minutes, and other usage charges. Render bills each HA standby with the same compute and storage as
its primary. Database storage cannot be reduced after it is increased.

References:

- [Blueprint projects and environments](https://render.com/docs/blueprint-spec)
- [Render service instance types](https://render.com/pricing)
- [Postgres storage](https://render.com/docs/postgresql-refresh)
- [Postgres high availability](https://render.com/docs/postgresql-high-availability)
- [Render API rate limits](https://api-docs.render.com/reference/rate-limiting)
