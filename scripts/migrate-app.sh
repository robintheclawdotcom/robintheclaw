#!/usr/bin/env bash
set -euo pipefail

migrations=(
  app/migrations/0001_product.sql
  app/migrations/0002_agents.sql
  app/migrations/0003_mainnet_agents.sql
  app/migrations/0004_live_agent_hardening.sql
  app/migrations/0005_command_dispatch.sql
  app/migrations/0006_robinhood_provisioning.sql
  app/migrations/0007_account_registration.sql
  app/migrations/0008_robinhood_authorization_proof.sql
  app/migrations/0009_repin_strategy_manifest.sql
  app/migrations/0010_repin_private_strategy_policy.sql
  app/migrations/0011_execution_account_generations.sql
)

if [[ "${1:-}" == "--check" ]]; then
  expected="$(printf '%s\n' "${migrations[@]}" | LC_ALL=C sort)"
  actual="$(find app/migrations -type f -name '*.sql' | LC_ALL=C sort)"
  [[ "$expected" == "$actual" ]] || {
    echo "application migration manifest does not match app/migrations" >&2
    diff -u <(printf '%s\n' "$expected") <(printf '%s\n' "$actual") || true
    exit 1
  }
  echo "application migration manifest is valid"
  exit 0
fi

bash scripts/run-ordered-migrations.sh \
  "${1:-}" robin_app_schema_migrations robin-app-schema "${migrations[@]}"
