#!/usr/bin/env bash
set -euo pipefail

migrations=(
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
  coordinator/migrations/0017_refresh_basis_aapl_canary.sql
  coordinator/migrations/0018_repin_private_strategy_policy.sql
  coordinator/migrations/0019_robinhood_snapshot_source_blocks.sql
  coordinator/migrations/0020_release_blocked_exit_quotes.sql
  coordinator/migrations/0021_require_runtime_readiness.sql
  runtime/live-scheduler/migrations/0001_live_scheduler.sql
  runtime/live-scheduler/migrations/0002_natural_strategy_exit.sql
  runtime/live-scheduler/migrations/0003_repin_strategy_manifest.sql
  runtime/live-scheduler/migrations/0004_repin_private_strategy_policy.sql
  runtime/live-evaluation/migrations/0001_live_evaluation.sql
  runtime/live-evaluation/migrations/0002_market_config_bootstrap.sql
  runtime/sequencer-publisher/migrations/0001_sequencer_journal.sql
  runtime/sequencer-publisher/migrations/0002_aapl_relay_journal.sql
  runtime/sequencer-publisher/migrations/0003_publisher_journal_isolation.sql
)

sha256_file() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$path" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 -r "$path" | awk '{print $1}'
  else
    echo "no SHA-256 utility is available" >&2
    return 1
  fi
}

if [[ "${1:-}" == "--check" ]]; then
  manifest="$(printf '%s\n' "${migrations[@]}" | LC_ALL=C sort)"
  discovered="$(
    find coordinator/migrations runtime/live-scheduler/migrations runtime/live-evaluation/migrations \
      runtime/sequencer-publisher/migrations \
      -type f -name '*.sql' | LC_ALL=C sort
  )"
  if [[ "$manifest" != "$discovered" ]]; then
    echo "execution migration manifest does not match migration directories" >&2
    diff -u <(printf '%s\n' "$manifest") <(printf '%s\n' "$discovered") || true
    exit 1
  fi
  for migration in "${migrations[@]}"; do
    if [[ ! -f "$migration" ]]; then
      echo "missing execution migration: $migration" >&2
      exit 1
    fi
    sha256_file "$migration" >/dev/null
  done
  echo "execution migration manifest is valid"
  exit 0
fi

database_url="${1:-}"
if [[ -z "$database_url" ]]; then
  echo "execution database URL is required" >&2
  exit 1
fi

ROBIN_DATABASE_URL="$database_url" ruby scripts/psql-with-url.rb --set ON_ERROR_STOP=1 <<'SQL'
BEGIN;
SELECT pg_advisory_xact_lock(hashtextextended('robin-execution-schema', 0));

DO $$
BEGIN
    IF to_regclass('public.execution_control') IS NOT NULL THEN
        EXECUTE $statement$
            UPDATE execution_control
            SET mode = 'HALTED',
                reason = 'deployment migration in progress',
                version = version + 1,
                updated_at = now()
            WHERE singleton
              AND (mode <> 'HALTED' OR reason <> 'deployment migration in progress')
        $statement$;
    END IF;
    IF to_regclass('public.execution_strategy_control') IS NOT NULL THEN
        EXECUTE $statement$
            UPDATE execution_strategy_control
            SET mode = 'HALTED',
                reason = 'deployment migration in progress',
                version = version + 1,
                updated_at = now()
            WHERE mode <> 'HALTED' OR reason <> 'deployment migration in progress'
        $statement$;
    END IF;
    IF to_regclass('public.execution_account_control') IS NOT NULL THEN
        EXECUTE $statement$
            UPDATE execution_account_control
            SET mode = 'HALTED',
                reason = 'deployment migration in progress',
                version = version + 1,
                updated_at = now()
            WHERE mode <> 'HALTED' OR reason <> 'deployment migration in progress'
        $statement$;
    END IF;
END;
$$;
COMMIT;
SQL

wrapper="$(mktemp)"
trap 'rm -f "$wrapper"' EXIT
{
  printf '\\set ON_ERROR_STOP on\n'
  printf "SELECT pg_advisory_lock(hashtextextended('robin-execution-schema', 0));\n"
  printf 'BEGIN;\n'
  cat <<'SQL'
CREATE TABLE IF NOT EXISTS robin_execution_schema_migrations (
    migration TEXT PRIMARY KEY,
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
SQL

for migration in "${migrations[@]}"; do
  if [[ ! -f "$migration" ]]; then
    echo "missing execution migration: $migration" >&2
    exit 1
  fi
  digest="$(sha256_file "$migration")"
  absolute_path="$(pwd)/$migration"
  printf "SELECT EXISTS (SELECT 1 FROM robin_execution_schema_migrations WHERE migration = '%s') AS applied \\\\gset\n" "$migration"
  printf '\\if :applied\n'
  printf "SELECT sha256 = '%s' AS checksum_valid FROM robin_execution_schema_migrations WHERE migration = '%s' \\\\gset\n" "$digest" "$migration"
  printf '\\if :checksum_valid\n'
  printf '\\else\n'
  printf '\\echo execution migration checksum mismatch\n'
  printf '%s\n' "DO \$\$ BEGIN RAISE EXCEPTION 'execution migration checksum mismatch'; END \$\$;"
  printf '\\endif\n'
  printf '\\else\n'
  printf '\\ir %s\n' "$absolute_path"
  printf "INSERT INTO robin_execution_schema_migrations (migration, sha256) VALUES ('%s', '%s');\n" "$migration" "$digest"
  printf '\\endif\n'
done

  printf '\\ir %s\n' "$(pwd)/scripts/lock-execution-after-migration.sql"
  printf 'COMMIT;\n'
  printf "SELECT pg_advisory_unlock(hashtextextended('robin-execution-schema', 0));\n"
} > "$wrapper"

ROBIN_DATABASE_URL="$database_url" ruby scripts/psql-with-url.rb --file "$wrapper"
rm -f "$wrapper"
trap - EXIT
