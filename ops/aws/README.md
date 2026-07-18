# AWS identity and KMS bootstrap

This stack creates the Render OIDC provider, service-specific IAM roles, the retained Lighter
credential-envelope key and alias, and the fixed Robinhood execution-key control plane. It contains
no credentials, execution-account IDs, or live trading parameters.

The Render Robinhood provisioner role can invoke one exact, retained Lambda version and has no KMS
permissions. The published version captures the reviewed source and configuration, so changing
`$LATEST` cannot change provisioning behavior. The function accepts only a canonical
execution-account UUIDv4. It derives the fixed alias, account-bound key policy, and key settings in
operator-controlled code and returns a public binding. Its retained DynamoDB ledger, single
concurrency, and non-retrying `CreateKey` client prevent handler or SDK retries from minting a
second key. An interrupted create without a recorded key ARN fails closed for operator
investigation.

Prerequisites:

- an AWS operator session allowed to create CloudFormation, IAM, OIDC, Lambda, DynamoDB, logs, and
  KMS resources;
- a Render Pro-or-higher workspace with the three services in one Render environment;
- the workspace ID, environment ID, and exact service ID for each provisioner and signer.

## Two-phase Render bootstrap

Render service IDs must exist before AWS can bind OIDC subjects, while the services must not run
with incomplete AWS bindings.

1. Create the three Render services with auto-deploy off, immediately call
   `POST /v1/services/{serviceId}/suspend` for each, and wait until all three service records report
   `suspended`. Do not attach live inputs, credentials, or capital before suspension is confirmed.
2. Move all three services into the same protected Render project environment. Top-level Blueprint
   resources do not automatically join an environment. Query
   `GET /v1/services?environmentId={environmentId}` and require the result to contain all three
   exact `srv-...` IDs. Record that `evm-...` ID; abort on an ungrouped service, a different
   environment, or a duplicate name.
3. Validate and deploy this stack with those exact service and `evm-...` environment IDs. The stack
   rejects the ungrouped `default` environment.
4. Set each stack output only on its matching service:
   - `LighterProvisionerRoleArn` as `AWS_ROLE_ARN` and `LighterEnvelopeKeyAlias` as
     `AWS_KMS_KEY_ID` on `robin-lighter-provisioner`;
   - `RobinhoodProvisionerRoleArn` as `AWS_ROLE_ARN` and `RobinhoodKeyControlPlaneArn` as
     `ROBINHOOD_KMS_PROVISION_FUNCTION_ARN` on `robin-robinhood-provisioner`;
   - `RobinhoodSignerRoleArn` as `AWS_ROLE_ARN` on `robin-robinhood-signer`.
5. Supply every other `sync: false` dependency. Re-query the environment membership and suspended
   state, then resume and explicitly deploy the three services. Do not set
   `AWS_WEB_IDENTITY_TOKEN_FILE`; Render supplies it after redeployment.

Every service using a Render private database must run in that database's region. The release keeps
the execution publishers and relays in Virginia with `robin-execution`; quorum independence comes
from separate publisher identities and RPC providers, not unreachable cross-region private URLs.

Validate and deploy:

```sh
ruby scripts/validate-aws-bootstrap.rb
ruby scripts/validate-aws-bootstrap.test.rb

aws cloudformation deploy \
  --template-file ops/aws/render-kms-bootstrap.yaml \
  --stack-name robin-mainnet-identity \
  --region us-east-1 \
  --capabilities CAPABILITY_IAM \
  --parameter-overrides \
    RenderWorkspaceId="$RENDER_WORKSPACE_ID" \
    RenderEnvironmentId="$RENDER_ENVIRONMENT_ID" \
    LighterProvisionerServiceId="$LIGHTER_PROVISIONER_SERVICE_ID" \
    RobinhoodProvisionerServiceId="$ROBINHOOD_PROVISIONER_SERVICE_ID" \
    RobinhoodSignerServiceId="$ROBINHOOD_SIGNER_SERVICE_ID"
```

Read the outputs:

```sh
aws cloudformation describe-stacks \
  --stack-name robin-mainnet-identity \
  --region us-east-1 \
  --query 'Stacks[0].Outputs[*].[OutputKey,OutputValue]' \
  --output table
```

The Lighter key and `alias/robin/lighter/credentials` are both retained. That alias is part of the
encryption contract and is not a stack parameter. If it must change, deploy a separately reviewed
release, decrypt each envelope through the old alias, re-encrypt through the new alias with the
same authenticated context, verify every record, then switch the service. Never repoint or delete
the retained alias in place.

The Robinhood key Lambda validates exact metadata, account-bound policy ID, policy statements,
aliases, and public-key algorithm on every call. It rejects extra request fields, wrong ledger
records, policy mismatches, and alias collisions. If the ledger is left in `CREATING` without a key
ARN, inspect CloudTrail and locate the key whose description and immutable policy ID contain that
exact execution-account UUID. Do not retry creation. After proving whether AWS created one key or
none, repair or remove the reservation as a deliberate operator recovery action.

Each key policy directly grants the exact signer role permission to read its public key and sign
32-byte digests with `ECDSA_SHA_256`. The signer role has no identity-based KMS allow. The
control-plane role can validate and alias only keys whose own policy names it, and has explicit
denies for signing, grants, policy changes, and tag changes. Replacing either IAM role intentionally
breaks historical key access; role replacement requires a reviewed key-policy migration before the
old role is removed. The Lambda execution role can choose an initial policy when it creates a key,
so permission to update this function or pass its role is custody-critical and must remain outside
the Render identities.
