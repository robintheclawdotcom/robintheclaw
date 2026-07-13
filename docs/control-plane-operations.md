# Control-plane operations

## Deployment boundary

`robin-control-api` is a Render private service. It must never be converted to a web service or
given a public hostname. It uses a bounded direct pool so its read-only session settings cannot be
lost through transaction pooling. The database role must also be read-only.

The process must not start unless all of the following are true:

- `APP_ENV=production`;
- `HOST` binds a non-loopback address;
- the generated control token is present;
- the database connection succeeds;
- capture, source-health, shadow-intent, strategy-candidate, and dataset-snapshot tables exist.

No execution or signer service is declared in `render.yaml`. Adding one is a release-boundary
change requiring an implemented binary, private-service threat model, independent key review,
incident controls, and a separate approval.

## Release procedure

1. Run `ruby scripts/validate-blueprint.rb` and the complete repository checks.
2. Review the Blueprint diff for service type, region, database reference, secret fields, and
   `checksPass` deployment policy.
3. Confirm the database has a current point-in-time recovery window and a healthy HA standby.
4. Merge only after all required GitHub checks pass.
5. Confirm Render deploys the expected commit and that `/livez` returns `live`.
6. Confirm `/readyz` returns `ready` before routing operator traffic.
7. Query the authenticated source-health, capture-summary, and metrics routes.
8. Record the commit, Render deploy identifier, schema version, and verification result in the
   release evidence.

Never put a control token in a shell history, URL, support message, screenshot, or repository.

## Database recovery drill

Run the drill quarterly and after material schema changes:

1. Restore the selected recovery point into an isolated database.
2. Keep the restored database unreachable from public IPs.
3. start a control API instance with a new token and the restored connection string.
4. Verify readiness, table counts, source-health recency, dataset manifests, and digest evidence.
5. Reconcile the restored database against R2 manifests for the same time boundary.
6. Delete the isolated resources after retaining non-sensitive drill evidence.

A successful SQL restore without R2 reconciliation is not a completed recovery drill.

## Incidents

| Condition | Immediate response | Recovery gate |
| --- | --- | --- |
| Readiness fails | Remove operator traffic and inspect private logs. | Required schema and database query both pass. |
| Unexpected authentication failures | Rotate the control token and review gateway and Render audit logs. | Source is identified and the old token is revoked. |
| Database write succeeds from the control service | Revoke its database credentials immediately. | A dedicated read-only role and session enforcement are independently verified. |
| Capture summary diverges from R2 | Mark the dataset incomplete and stop promotion clocks. | Database and archive manifests reconcile. |
| Postgres failover | Verify connection recovery and query continuity. | Readiness and reconciliation pass after the new primary stabilizes. |
| Schema changes break the API | Roll back the API, not the evidence database. | Compatibility is restored through a reviewed forward migration. |

## Required external configuration

Repository changes do not perform these account-level actions:

- upgrade the Render workspace and `robin-research` database for Pro HA;
- attach and sync the Blueprint;
- enable GitHub branch protection with all CI jobs required;
- place an authenticated gateway in front of any future operator interface;
- route metrics and logs to the selected OpenTelemetry backend;
- configure paging destinations and escalation ownership;
- schedule logical exports and quarterly restore exercises.
