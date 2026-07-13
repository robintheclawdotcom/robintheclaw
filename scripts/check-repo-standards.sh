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
  scripts/setup-git-hooks.sh
  scripts/validate-blueprint.rb
  config/mainnet-live-policy.json
  config/strategies/basis-aapl-v1.manifest.json
  config/strategies/basis-aapl-v1.oracle-policy.json
  config/strategies/basis-aapl-v1.risk-policy.json
  config/strategies/basis-aapl-v1.route.json
  config/validate-live-policy.mjs
  config/validate-live-policy.test.mjs
  scripts/validate-mainnet-release.sh
  docs/production-audit-mainnet-live-execution.md
  liveexec/scripts/validate.sh
  ops/mainnet-live/validate.mjs
  ops/mainnet-live/promotion-ledger/cli.mjs
  ops/mainnet-live/promotion-ledger/ledger.mjs
  ops/mainnet-live/promotion-ledger/ledger.test.mjs
  publisher/cmd/account-publisher/main.go
  provisioner/lighter/main.go
  provisioner/robinhood/main.go
  signer/lighter/main.go
  signer/robinhood/main.go
  runtime/Cargo.toml
  runtime/migrations/0001_capture.sql
  runtime/src/bin/collector.rs
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
