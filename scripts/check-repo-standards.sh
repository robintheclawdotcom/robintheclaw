#!/usr/bin/env bash
# Verifies the public repository carries the expected governance and policy files.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

req=(
  LICENSE
  README.md
  SECURITY.md
  CONTRIBUTING.md
  CODE_OF_CONDUCT.md
  GOVERNANCE.md
  MAINTAINERS.md
  SUPPORT.md
  RELEASING.md
  AGENTS.md
  .githooks/pre-commit
  .githooks/pre-push
  scripts/check-git-identity.sh
  scripts/check-no-leaks.sh
  scripts/check-node-packages.sh
  scripts/check-rust-crates.sh
  scripts/generate-mainnet-operator-keys.mjs
  scripts/generate-mainnet-operator-keys.test.mjs
  scripts/generate-mainnet-strategy-policy.mjs
  scripts/generate-mainnet-strategy-policy.test.mjs
  scripts/database-authority.integration.test.sh
  scripts/database-runtime-exec.rb
  scripts/database-runtime-exec.test.rb
  scripts/lock-execution-after-migration.sql
  scripts/migrate-app.sh
  scripts/migrate-lighter-provisioner.sh
  scripts/migrate-research.sh
  scripts/migrate-robinhood-provisioner.sh
  scripts/prepare-database.sh
  scripts/provision-database-roles.sh
  scripts/provision-database-roles.test.sh
  scripts/psql-with-url.rb
  scripts/psql-with-url.test.rb
  scripts/run-ordered-migrations.sh
  scripts/setup-git-hooks.sh
  scripts/validate-blueprint.rb
  scripts/validate-blueprint.test.rb
  scripts/validate-aws-bootstrap.rb
  scripts/validate-aws-bootstrap.test.rb
  config/mainnet-live-policy.json
  config/render-external-env-groups.example.json
  config/strategies/basis-aapl-v1.manifest.json
  config/strategies/basis-aapl-v1.oracle-policy.json
  config/strategies/basis-aapl-v1.risk-policy.json
  config/strategies/basis-aapl-v1.route.json
  config/validate-live-policy.mjs
  config/validate-live-policy.test.mjs
  config/engineering-canary-evidence.json
  scripts/migrate-execution.sh
  scripts/migrate-checksums.test.sh
  scripts/migrate-robinhood-signer.sh
  scripts/provision-render-env-groups.mjs
  scripts/provision-render-env-groups.test.mjs
  scripts/render-mainnet-bootstrap.rb
  scripts/render-mainnet-bootstrap.test.rb
  runtime/live-control/go.mod
  runtime/live-control/main.go
  runtime/live-control/main_test.go
  coordinator/migrations/0017_refresh_basis_aapl_canary.sql
  coordinator/migrations/0018_repin_private_strategy_policy.sql
  coordinator/migrations/0019_robinhood_snapshot_source_blocks.sql
  coordinator/migrations/0020_release_blocked_exit_quotes.sql
  coordinator/migrations/0021_require_runtime_readiness.sql
  scripts/validate-engineering-canary.mjs
  scripts/validate-engineering-canary.test.mjs
  scripts/validate-mainnet-release.sh
  docs/database-authority.md
  docs/production-audit-mainnet-live-execution.md
  docs/render-mainnet-bootstrap.md
  ops/aws/README.md
  ops/aws/render-kms-bootstrap.yaml
  liveexec/scripts/validate.sh
  ops/mainnet-live/validate.mjs
  ops/mainnet-live/promotion-ledger/cli.mjs
  ops/mainnet-live/promotion-ledger/ledger.mjs
  ops/mainnet-live/promotion-ledger/ledger.test.mjs
  publisher/cmd/account-publisher/main.go
  runtime/exit-quote-publisher/cmd/exit-quote-publisher/main.go
  runtime/live-evaluation/cmd/live-evaluation/main.go
  runtime/live-evaluation/migrations/0001_live_evaluation.sql
  runtime/live-scheduler/cmd/live-scheduler/main.go
  runtime/live-scheduler/migrations/0004_repin_private_strategy_policy.sql
  runtime/sequencer-publisher/dependencies.go
  runtime/sequencer-publisher/migrations/0001_sequencer_journal.sql
  runtime/sequencer-publisher/migrations/0002_aapl_relay_journal.sql
  runtime/sequencer-publisher/cmd/aapl-relay/main.go
  runtime/sequencer-publisher/cmd/sequencer-publisher/main.go
  provisioner/lighter/main.go
  provisioner/robinhood/main.go
  signer/lighter/main.go
  signer/robinhood/main.go
  runtime/Cargo.toml
  runtime/migrations/0001_capture.sql
  runtime/migrations/0005_secure_archive_partition_function.sql
  runtime/src/bin/collector.rs
  app/migrations/0010_repin_private_strategy_policy.sql
  app/migrations/0011_execution_account_generations.sql
  .github/CODEOWNERS
  .github/PULL_REQUEST_TEMPLATE.md
  .github/dependabot.yml
  .github/workflows/ci.yml
  .github/workflows/identity-firewall.yml
  render.yaml
)

miss=0
for f in "${req[@]}"; do
  if [ ! -f "$f" ]; then
    echo "missing: $f"
    miss=1
  fi
done

if [ "$miss" -eq 0 ]; then
  echo "repo standards: ok"
else
  echo "repo standards: FAILED"
  exit 1
fi
