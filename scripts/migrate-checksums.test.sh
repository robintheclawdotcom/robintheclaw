#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
execution_url="${EXECUTION_MIGRATION_TEST_DATABASE_URL:-}"
signer_url="${SIGNER_MIGRATION_TEST_DATABASE_URL:-}"

if [[ -z "$execution_url" || -z "$signer_url" ]]; then
  echo "execution and signer migration test database URLs are required" >&2
  exit 1
fi

workspace="$(mktemp -d)"
trap 'rm -rf "$workspace"' EXIT

mkdir -p \
  "$workspace/scripts" \
  "$workspace/coordinator" \
  "$workspace/runtime/live-scheduler" \
  "$workspace/runtime/live-evaluation" \
  "$workspace/runtime/sequencer-publisher" \
  "$workspace/signer/robinhood"

cp "$root/scripts/migrate-execution.sh" "$workspace/scripts/"
cp "$root/scripts/migrate-robinhood-signer.sh" "$workspace/scripts/"
cp "$root/scripts/lock-execution-after-migration.sql" "$workspace/scripts/"
cp "$root/scripts/psql-with-url.rb" "$workspace/scripts/"
cp "$root/scripts/run-ordered-migrations.sh" "$workspace/scripts/"
cp -R "$root/coordinator/migrations" "$workspace/coordinator/"
cp -R "$root/runtime/live-scheduler/migrations" "$workspace/runtime/live-scheduler/"
cp -R "$root/runtime/live-evaluation/migrations" "$workspace/runtime/live-evaluation/"
cp -R "$root/runtime/sequencer-publisher/migrations" "$workspace/runtime/sequencer-publisher/"
cp -R "$root/signer/robinhood/migrations" "$workspace/signer/robinhood/"

assert_checksum_failure() {
  local runner="$1"
  local database_url="$2"
  local migration="$3"
  local message="$4"
  local log="$workspace/checksum-failure.log"

  (
    cd "$workspace"
    bash "$runner" "$database_url" >/dev/null
    printf '\n-- checksum regression mutation\n' >> "$migration"
    if bash "$runner" "$database_url" >"$log" 2>&1; then
      echo "$runner accepted a changed applied migration" >&2
      exit 1
    fi
  )

  if ! grep -Fq "$message" "$log"; then
    echo "$runner failed without the checksum mismatch error" >&2
    cat "$log" >&2
    exit 1
  fi
}

assert_execution_atomicity() {
  local log="$workspace/execution-atomicity.log"
  local partial=(
    coordinator/migrations/0001_execution.sql
    coordinator/migrations/0002_execution_actions.sql
    coordinator/migrations/0003_venue_event_binding.sql
    coordinator/migrations/0004_market_authority.sql
    coordinator/migrations/0005_exit_authority.sql
    coordinator/migrations/0006_multi_account_execution.sql
    coordinator/migrations/0007_account_commands.sql
    coordinator/migrations/0008_account_registration.sql
    coordinator/migrations/0009_intent_idempotency.sql
    coordinator/migrations/0010_exit_dispatch.sql
    coordinator/migrations/0011_operator_restrictions.sql
    coordinator/migrations/0012_internal_canary_promotion.sql
    coordinator/migrations/0013_derived_canary_readiness.sql
    coordinator/migrations/0014_open_episode_resolution.sql
    coordinator/migrations/0015_exit_execution_policy.sql
    coordinator/migrations/0016_enable_basis_aapl_canary.sql
  )

  (
    cd "$workspace"
    bash scripts/run-ordered-migrations.sh \
      "$execution_url" robin_execution_schema_migrations robin-execution-schema \
      "${partial[@]}" >/dev/null
    printf '\nDO $$ BEGIN RAISE EXCEPTION %s; END $$;\n' \
      "'execution atomicity regression marker'" \
      >> coordinator/migrations/0019_robinhood_snapshot_source_blocks.sql
    if bash scripts/migrate-execution.sh "$execution_url" >"$log" 2>&1; then
      echo "execution migration accepted an injected pending failure" >&2
      exit 1
    fi
  )

  if ! grep -Fq "execution atomicity regression marker" "$log"; then
    echo "execution migration did not surface the injected pending failure" >&2
    cat "$log" >&2
    exit 1
  fi

  local pending_applied
  pending_applied="$(
    ROBIN_DATABASE_URL="$execution_url" ruby "$root/scripts/psql-with-url.rb" -Atc \
      "SELECT count(*) FROM robin_execution_schema_migrations
       WHERE migration IN (
         'coordinator/migrations/0017_refresh_basis_aapl_canary.sql',
         'coordinator/migrations/0018_repin_private_strategy_policy.sql'
       )"
  )"
  if [[ "$pending_applied" != "0" ]]; then
    echo "failed execution chain committed migrations before the failure" >&2
    exit 1
  fi

  local mode
  mode="$(
    ROBIN_DATABASE_URL="$execution_url" ruby "$root/scripts/psql-with-url.rb" -Atc \
      "SELECT mode || '|' || reason FROM execution_control WHERE singleton"
  )"
  if [[ "$mode" != "HALTED|deployment migration in progress" ]]; then
    echo "failed execution chain did not preserve the fail-safe halt" >&2
    exit 1
  fi

  cp \
    "$root/coordinator/migrations/0019_robinhood_snapshot_source_blocks.sql" \
    "$workspace/coordinator/migrations/0019_robinhood_snapshot_source_blocks.sql"
  (
    cd "$workspace"
    bash scripts/migrate-execution.sh "$execution_url" >/dev/null
  )
}

assert_execution_atomicity

assert_checksum_failure \
  scripts/migrate-execution.sh \
  "$execution_url" \
  coordinator/migrations/0020_release_blocked_exit_quotes.sql \
  "execution migration checksum mismatch"

mode="$(
  ROBIN_DATABASE_URL="$execution_url" ruby "$root/scripts/psql-with-url.rb" -Atc \
    "SELECT mode || '|' || reason FROM execution_control WHERE singleton"
)"
if [[ "$mode" != "HALTED|deployment migration in progress" ]]; then
  echo "failed execution migration did not leave the control plane halted" >&2
  exit 1
fi

assert_checksum_failure \
  scripts/migrate-robinhood-signer.sh \
  "$signer_url" \
  signer/robinhood/migrations/0001_journal.sql \
  "migration checksum mismatch: signer/robinhood/migrations/0001_journal.sql"

echo "migration checksum regressions: ok"
