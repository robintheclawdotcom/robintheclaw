#!/usr/bin/env bash
set -euo pipefail

app_url="${APP_DATABASE_AUTHORITY_TEST_URL:-}"
research_url="${RESEARCH_DATABASE_AUTHORITY_TEST_URL:-}"
execution_url="${EXECUTION_DATABASE_AUTHORITY_TEST_URL:-}"
lighter_url="${LIGHTER_DATABASE_AUTHORITY_TEST_URL:-}"
custody_url="${CUSTODY_DATABASE_AUTHORITY_TEST_URL:-}"
role_password="database-role-password-000000000000000000000000"

for required in app_url research_url execution_url lighter_url custody_url; do
  if [[ -z "${!required}" ]]; then
    echo "${required%_url} database authority test URL is required" >&2
    exit 1
  fi
done

runtime_url() {
  ruby -r ./scripts/database-runtime-exec.rb -e \
    'print DatabaseRuntime.runtime_url(ARGV[0], ARGV[1], ARGV[2])' \
    "$1" "$2" "$role_password"
}

owner_sql() {
  local url="$1"
  shift
  ROBIN_DATABASE_URL="$url" ruby scripts/psql-with-url.rb --set ON_ERROR_STOP=1 "$@"
}

role_sql() {
  local owner="$1"
  local role="$2"
  shift 2
  local url
  url="$(runtime_url "$owner" "$role")"
  ROBIN_DATABASE_URL="$url" ruby scripts/psql-with-url.rb --set ON_ERROR_STOP=1 "$@"
}

provision() {
  ROBIN_DATABASE_PASSWORD="$role_password" \
    bash scripts/provision-database-roles.sh "$1" "$2" >/dev/null
}

assert_privileges() {
  local owner="$1"
  local role="$2"
  local privilege="$3"
  shift 3
  local values=""
  local table
  for table in "$@"; do
    values+="'$table',"
  done
  values="${values%,}"

  local missing
  missing="$(
    role_sql "$owner" "$role" -Atc "
      SELECT coalesce(string_agg(name, ',' ORDER BY name), '')
      FROM unnest(ARRAY[$values]::text[]) expected(name)
      WHERE NOT has_table_privilege(
        current_user,
        format('public.%I', name),
        '$privilege'
      )
    "
  )"
  if [[ -n "$missing" ]]; then
    echo "$role lacks $privilege on: $missing" >&2
    exit 1
  fi
}

assert_denied() {
  local owner="$1"
  local role="$2"
  local privilege="$3"
  local table="$4"
  local granted
  granted="$(
    role_sql "$owner" "$role" -Atc \
      "SELECT has_table_privilege(current_user, 'public.$table', '$privilege')"
  )"
  if [[ "$granted" != "f" ]]; then
    echo "$role unexpectedly has $privilege on $table" >&2
    exit 1
  fi
}

assert_role_baseline() {
  local owner="$1"
  local role="$2"
  local invalid
  invalid="$(
    owner_sql "$owner" -Atc "
      SELECT count(*)
      FROM pg_roles role_record
      WHERE role_record.rolname = '$role'
        AND (
          role_record.rolsuper
          OR role_record.rolcreatedb
          OR role_record.rolcreaterole
          OR role_record.rolinherit
          OR role_record.rolreplication
          OR role_record.rolbypassrls
          OR has_database_privilege('$role', current_database(), 'TEMPORARY')
          OR has_schema_privilege('$role', 'public', 'CREATE')
          OR EXISTS (
            SELECT 1 FROM pg_auth_members membership
            WHERE membership.member = role_record.oid
          )
        )
    "
  )"
  if [[ "$invalid" != "0" ]]; then
    echo "$role has elevated database authority" >&2
    exit 1
  fi
}

assert_security_definers() {
  local owner="$1"
  local role="$2"
  local expected="$3"
  local actual
  actual="$(
    owner_sql "$owner" -Atc "
      SELECT coalesce(string_agg(routine.oid::regprocedure::text, ',' ORDER BY routine.oid::regprocedure::text), '')
      FROM pg_proc routine
      JOIN pg_namespace namespace ON namespace.oid = routine.pronamespace
      WHERE namespace.nspname = 'public'
        AND routine.prosecdef
        AND has_function_privilege('$role', routine.oid, 'EXECUTE')
    "
  )"
  if [[ "$actual" != "$expected" ]]; then
    echo "$role has unexpected security-definer authority: $actual" >&2
    exit 1
  fi
}

expect_role_failure() {
  local owner="$1"
  local role="$2"
  local expected="$3"
  shift 3
  local log
  log="$(mktemp)"
  if role_sql "$owner" "$role" "$@" >"$log" 2>&1; then
    echo "$role operation unexpectedly succeeded" >&2
    rm -f "$log"
    exit 1
  fi
  if ! grep -Fq "$expected" "$log"; then
    cat "$log" >&2
    rm -f "$log"
    exit 1
  fi
  rm -f "$log"
}

expect_lock_failure() {
  local expected="$1"
  local log
  log="$(mktemp)"
  if owner_sql "$execution_url" --single-transaction \
      --file scripts/lock-execution-after-migration.sql >"$log" 2>&1; then
    echo "execution quiescence unexpectedly succeeded" >&2
    rm -f "$log"
    exit 1
  fi
  if ! grep -Fq "$expected" "$log"; then
    cat "$log" >&2
    rm -f "$log"
    exit 1
  fi
  rm -f "$log"
}

bash scripts/migrate-app.sh "$app_url" >/dev/null
bash scripts/migrate-research.sh "$research_url" >/dev/null
bash scripts/migrate-execution.sh "$execution_url" >/dev/null
bash scripts/migrate-lighter-provisioner.sh "$lighter_url" >/dev/null
bash scripts/migrate-robinhood-provisioner.sh "$custody_url" >/dev/null
bash scripts/migrate-robinhood-signer.sh "$custody_url" >/dev/null

owner_sql "$execution_url" >/dev/null <<'SQL'
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'robin_execution_sequencer') THEN
        CREATE ROLE robin_execution_sequencer;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'robin_execution_aapl_relay') THEN
        CREATE ROLE robin_execution_aapl_relay;
    END IF;
END;
$$;
ALTER ROLE robin_execution_sequencer LOGIN
    PASSWORD 'deprecated-sequencer-password-0000000000000000'
    NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS;
ALTER ROLE robin_execution_aapl_relay LOGIN
    PASSWORD 'deprecated-relay-password-000000000000000000000'
    NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS;
SELECT format(
    'GRANT CONNECT ON DATABASE %I TO robin_execution_sequencer, robin_execution_aapl_relay',
    current_database()
)
\gexec
GRANT USAGE ON SCHEMA public
    TO robin_execution_sequencer, robin_execution_aapl_relay;
GRANT ALL ON sequencer_publisher_state TO robin_execution_sequencer;
GRANT ALL ON aapl_relay_state TO robin_execution_aapl_relay;
SQL

for role in robin_app_api robin_app_paper robin_app_readonly; do
  provision "$app_url" "$role"
done
for role in robin_research_collector robin_research_paper robin_research_readonly; do
  provision "$research_url" "$role"
done
for role in \
  robin_execution_coordinator robin_execution_live_control \
  robin_execution_sequencer_1 robin_execution_sequencer_2 robin_execution_sequencer_3 \
  robin_execution_aapl_relay_1 robin_execution_aapl_relay_2 robin_execution_aapl_relay_3 \
  robin_execution_readonly; do
  provision "$execution_url" "$role"
done
for role in robin_lighter_provisioner robin_lighter_readonly; do
  provision "$lighter_url" "$role"
done
for role in robin_custody_provisioner robin_custody_signer robin_custody_readonly; do
  provision "$custody_url" "$role"
done

deprecated_access="$(
  owner_sql "$execution_url" -Atc "
    SELECT count(*)
    FROM pg_roles role_record
    WHERE role_record.rolname IN (
      'robin_execution_sequencer',
      'robin_execution_aapl_relay'
    )
      AND (
        role_record.rolcanlogin
        OR has_database_privilege(role_record.rolname, current_database(), 'CONNECT')
        OR has_schema_privilege(role_record.rolname, 'public', 'USAGE')
        OR has_table_privilege(
          role_record.rolname,
          CASE role_record.rolname
            WHEN 'robin_execution_sequencer' THEN 'sequencer_publisher_state'
            ELSE 'aapl_relay_state'
          END,
          'SELECT'
        )
      )
  "
)"
[[ "$deprecated_access" == "0" ]] || {
  echo "deprecated shared publisher role remains usable" >&2
  exit 1
}

for role in robin_app_api robin_app_paper robin_app_readonly; do
  assert_role_baseline "$app_url" "$role"
  assert_security_definers "$app_url" "$role" ""
done
for role in robin_research_collector robin_research_paper robin_research_readonly; do
  assert_role_baseline "$research_url" "$role"
  expected=""
  if [[ "$role" == "robin_research_collector" ]]; then
    expected="ensure_event_staging_partition(timestamp with time zone)"
  fi
  assert_security_definers "$research_url" "$role" "$expected"
done
for role in \
  robin_execution_coordinator robin_execution_live_control \
  robin_execution_sequencer_1 robin_execution_sequencer_2 robin_execution_sequencer_3 \
  robin_execution_aapl_relay_1 robin_execution_aapl_relay_2 robin_execution_aapl_relay_3 \
  robin_execution_readonly; do
  assert_role_baseline "$execution_url" "$role"
  assert_security_definers "$execution_url" "$role" ""
done
for role in robin_lighter_provisioner robin_lighter_readonly; do
  assert_role_baseline "$lighter_url" "$role"
  assert_security_definers "$lighter_url" "$role" ""
done
for role in robin_custody_provisioner robin_custody_signer robin_custody_readonly; do
  assert_role_baseline "$custody_url" "$role"
  assert_security_definers "$custody_url" "$role" ""
done

for role in robin_app_api robin_app_paper robin_app_readonly; do
  assert_denied "$app_url" "$role" SELECT robin_app_schema_migrations
done
for role in robin_research_collector robin_research_paper robin_research_readonly; do
  assert_denied "$research_url" "$role" SELECT robin_research_schema_migrations
done
for role in \
  robin_execution_coordinator robin_execution_live_control \
  robin_execution_sequencer_1 robin_execution_sequencer_2 robin_execution_sequencer_3 \
  robin_execution_aapl_relay_1 robin_execution_aapl_relay_2 robin_execution_aapl_relay_3 \
  robin_execution_readonly; do
  assert_denied "$execution_url" "$role" SELECT robin_execution_schema_migrations
done
for role in robin_lighter_provisioner robin_lighter_readonly; do
  assert_denied "$lighter_url" "$role" SELECT robin_lighter_schema_migrations
done
for role in robin_custody_provisioner robin_custody_signer robin_custody_readonly; do
  assert_denied "$custody_url" "$role" SELECT robin_robinhood_schema_migrations
  assert_denied "$custody_url" "$role" SELECT robin_signer_schema_migrations
done

assert_privileges "$app_url" robin_app_api SELECT \
  users wallet_links smart_accounts vaults activity app_cursors product_metrics \
  preferences agents agent_paper_events agent_readiness agent_readiness_evidence \
  agent_readiness_snapshots execution_accounts execution_account_bindings \
  agent_commands agent_command_outbox app_internal_nonces \
  coordinator_account_registrations coordinator_account_registration_outbox
assert_privileges "$app_url" robin_app_api INSERT users agents agent_commands
assert_privileges "$app_url" robin_app_paper SELECT agents agent_paper_events
assert_privileges "$app_url" robin_app_paper INSERT agent_paper_events
assert_denied "$app_url" robin_app_paper INSERT users
assert_privileges "$app_url" robin_app_readonly SELECT \
  users agents agent_readiness execution_accounts
assert_denied "$app_url" robin_app_readonly INSERT users

assert_privileges "$research_url" robin_research_collector SELECT \
  raw_market_events market_features source_health event_staging archive_segments \
  archive_segment_events archive_manifests
assert_privileges "$research_url" robin_research_collector INSERT \
  raw_market_events market_features source_health event_staging archive_segments \
  archive_segment_events archive_manifests
assert_denied "$research_url" robin_research_collector INSERT paper_evaluations
assert_privileges "$research_url" robin_research_paper SELECT \
  raw_market_events paper_agent_cursors paper_evaluations paper_market_state \
  paper_opportunity_episodes agent_fanout_outbox
assert_privileges "$research_url" robin_research_paper INSERT \
  paper_agent_cursors paper_evaluations paper_market_state \
  paper_opportunity_episodes agent_fanout_outbox
assert_denied "$research_url" robin_research_paper INSERT raw_market_events
assert_privileges "$research_url" robin_research_readonly SELECT \
  raw_market_events market_features paper_evaluations
assert_denied "$research_url" robin_research_readonly INSERT raw_market_events

assert_privileges "$execution_url" robin_execution_coordinator SELECT \
  execution_accounts execution_account_snapshots execution_intents execution_actions \
  execution_control execution_strategy_control execution_account_control \
  execution_market_quotes live_scheduler_work
assert_privileges "$execution_url" robin_execution_coordinator INSERT \
  execution_api_nonces execution_account_snapshots execution_intents execution_actions
assert_denied "$execution_url" robin_execution_coordinator INSERT live_scheduler_approvals
assert_privileges "$execution_url" robin_execution_live_control SELECT \
  execution_accounts execution_account_snapshots execution_market_configs \
  live_scheduler_approvals live_scheduler_work live_scheduler_events \
  live_execution_episode_bindings live_strategy_exit_bindings \
  live_evaluation_order_cursors live_evaluation_order_allocations
assert_privileges "$execution_url" robin_execution_live_control INSERT \
  execution_market_review_records execution_market_review_observations \
  live_scheduler_approvals live_scheduler_events live_execution_episode_bindings \
  live_strategy_exit_bindings live_evaluation_order_cursors \
  live_evaluation_order_allocations
assert_privileges "$execution_url" robin_execution_live_control UPDATE \
  execution_market_configs live_scheduler_work live_evaluation_order_cursors
assert_denied "$execution_url" robin_execution_live_control UPDATE execution_control
assert_denied "$execution_url" robin_execution_live_control UPDATE execution_signer_requests
for role in \
  robin_execution_sequencer_1 robin_execution_sequencer_2 robin_execution_sequencer_3; do
  assert_privileges "$execution_url" "$role" INSERT \
    sequencer_publisher_state sequencer_publisher_transactions
  assert_denied "$execution_url" "$role" DELETE sequencer_publisher_transactions
  assert_denied "$execution_url" "$role" INSERT aapl_relay_state
done
for role in \
  robin_execution_aapl_relay_1 robin_execution_aapl_relay_2 robin_execution_aapl_relay_3; do
  assert_privileges "$execution_url" "$role" INSERT \
    aapl_relay_state aapl_relay_transactions
  assert_denied "$execution_url" "$role" DELETE aapl_relay_transactions
  assert_denied "$execution_url" "$role" INSERT sequencer_publisher_state
done
assert_privileges "$execution_url" robin_execution_readonly SELECT \
  execution_accounts execution_account_snapshots live_scheduler_work
assert_denied "$execution_url" robin_execution_readonly INSERT execution_api_nonces

assert_privileges "$lighter_url" robin_lighter_provisioner SELECT \
  lighter_credentials lighter_credential_bindings lighter_credential_audit \
  lighter_provisioner_request_nonces lighter_signing_requests
assert_privileges "$lighter_url" robin_lighter_provisioner INSERT \
  lighter_credentials lighter_credential_bindings lighter_credential_audit \
  lighter_provisioner_request_nonces lighter_signing_requests
assert_privileges "$lighter_url" robin_lighter_readonly SELECT lighter_signing_requests
assert_denied "$lighter_url" robin_lighter_readonly SELECT lighter_credentials
assert_denied "$lighter_url" robin_lighter_readonly SELECT lighter_credential_bindings
assert_denied "$lighter_url" robin_lighter_readonly SELECT lighter_credential_audit
assert_denied "$lighter_url" robin_lighter_readonly INSERT lighter_provisioner_request_nonces

assert_privileges "$custody_url" robin_custody_provisioner INSERT \
  robinhood_execution_bindings robinhood_provisioner_auth_nonces robinhood_provisioner_audit
assert_denied "$custody_url" robin_custody_provisioner INSERT robinhood_signer_deployments
assert_privileges "$custody_url" robin_custody_signer INSERT \
  robinhood_signer_deployments robinhood_signer_nonces \
  robinhood_signer_transactions robinhood_signer_auth_nonces
assert_denied "$custody_url" robin_custody_signer INSERT robinhood_provisioner_auth_nonces
assert_privileges "$custody_url" robin_custody_readonly SELECT \
  robinhood_execution_bindings robinhood_provisioner_audit \
  robinhood_signer_deployments robinhood_signer_transactions
assert_denied "$custody_url" robin_custody_readonly INSERT robinhood_provisioner_auth_nonces

role_sql "$app_url" robin_app_api >/dev/null <<'SQL'
BEGIN;
INSERT INTO users (id, privy_did)
VALUES ('10000000-0000-4000-8000-000000000001', 'did:privy:authority-api');
INSERT INTO agents (id, user_id, strategy_version, mode, status)
VALUES (
    '10000000-0000-4000-8000-000000000002',
    '10000000-0000-4000-8000-000000000001',
    'basis-aapl-v1',
    'paper',
    'setup'
);
ROLLBACK;
SQL

owner_sql "$app_url" >/dev/null <<'SQL'
INSERT INTO users (id, privy_did)
VALUES ('10000000-0000-4000-8000-000000000011', 'did:privy:authority-paper');
INSERT INTO agents (id, user_id, strategy_version, mode, status)
VALUES (
    '10000000-0000-4000-8000-000000000012',
    '10000000-0000-4000-8000-000000000011',
    'basis-aapl-v1',
    'paper',
    'running'
);
SQL

role_sql "$app_url" robin_app_paper >/dev/null <<'SQL'
BEGIN;
INSERT INTO agent_paper_events (
    id, agent_id, evaluation_id, market_event_id, symbol, status, evaluated_at
) VALUES (
    '10000000-0000-4000-8000-000000000013',
    '10000000-0000-4000-8000-000000000012',
    '10000000-0000-4000-8000-000000000014',
    '10000000-0000-4000-8000-000000000015',
    'AAPL',
    'declined',
    now()
);
ROLLBACK;
SQL
role_sql "$app_url" robin_app_readonly -Atc "SELECT count(*) FROM agents" >/dev/null
expect_role_failure "$app_url" robin_app_paper "permission denied" \
  -c "INSERT INTO users (id, privy_did) VALUES ('10000000-0000-4000-8000-000000000016', 'did:privy:denied')"

role_sql "$research_url" robin_research_collector >/dev/null <<'SQL'
BEGIN;
SET LOCAL TIME ZONE 'Pacific/Kiritimati';
SELECT ensure_event_staging_partition(
    (date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
    - interval '30 minutes'
);
DO $$
DECLARE
    event_time TIMESTAMPTZ :=
        (date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
        - interval '30 minutes';
    expected_partition TEXT := format(
        'public.event_staging_y%sm%s',
        to_char(event_time AT TIME ZONE 'UTC', 'YYYY'),
        to_char(event_time AT TIME ZONE 'UTC', 'MM')
    );
BEGIN
    IF to_regclass(expected_partition) IS NULL THEN
        RAISE EXCEPTION 'staging partition boundaries depend on the caller timezone';
    END IF;
END;
$$;
SELECT ensure_event_staging_partition(now());
INSERT INTO raw_market_events (
    id, source, connector_version, kind, symbol, received_at, finality,
    payload_sha256, raw_object_key, payload, source_session, source_event_id
) VALUES (
    '20000000-0000-4000-8000-000000000001',
    'authority-test',
    'v1',
    'ticker',
    'AAPL',
    now(),
    'not_applicable',
    repeat('1', 64),
    'authority/test',
    '{}'::jsonb,
    'authority-session',
    'authority-event'
);
INSERT INTO event_staging (event_id, received_at, raw_payload)
VALUES (
    '20000000-0000-4000-8000-000000000001',
    now(),
    decode('00', 'hex')
);
INSERT INTO source_health (source, status, last_event_at)
VALUES ('authority-test', 'healthy', now());
ROLLBACK;
SQL
expect_role_failure "$research_url" robin_research_collector \
  "staging partition time is outside the capture window" \
  -c "SELECT ensure_event_staging_partition(TIMESTAMPTZ '2000-01-01')"
expect_role_failure "$research_url" robin_research_paper "permission denied" \
  -c "SELECT ensure_event_staging_partition(now())"

owner_sql "$research_url" >/dev/null <<'SQL'
INSERT INTO raw_market_events (
    id, source, connector_version, kind, symbol, received_at, finality,
    payload_sha256, raw_object_key, payload, source_session, source_event_id
) VALUES (
    '20000000-0000-4000-8000-000000000011',
    'authority-paper',
    'v1',
    'ticker',
    'AAPL',
    now(),
    'not_applicable',
    repeat('2', 64),
    'authority/paper',
    '{}'::jsonb,
    'authority-paper-session',
    'authority-paper-event'
);
SQL

role_sql "$research_url" robin_research_paper >/dev/null <<'SQL'
BEGIN;
INSERT INTO paper_agent_cursors (
    consumer, symbol, last_received_at, last_event_id
) VALUES (
    'authority-paper-agent',
    'AAPL',
    now(),
    '20000000-0000-4000-8000-000000000011'
);
INSERT INTO paper_evaluations (
    id, strategy_version, event_id, symbol, status, reason, evidence, evaluated_at
) VALUES (
    '20000000-0000-4000-8000-000000000012',
    'basis-aapl-v1',
    '20000000-0000-4000-8000-000000000011',
    'AAPL',
    'declined',
    'authority test',
    '{}'::jsonb,
    now()
);
INSERT INTO agent_fanout_outbox (
    evaluation_id, strategy_version, market_event_id, symbol, status, reason, evaluated_at
) VALUES (
    '20000000-0000-4000-8000-000000000012',
    'basis-aapl-v1',
    '20000000-0000-4000-8000-000000000011',
    'AAPL',
    'declined',
    'authority test',
    now()
);
ROLLBACK;
SQL
role_sql "$research_url" robin_research_readonly -Atc \
  "SELECT count(*) FROM raw_market_events" >/dev/null

role_sql "$execution_url" robin_execution_coordinator >/dev/null <<'SQL'
BEGIN;
UPDATE execution_control
SET reason = 'database authority integration test', version = version + 1
WHERE singleton;
INSERT INTO execution_api_nonces (scope, nonce, expires_at)
VALUES ('intent', 'authority-integration-nonce', now() + interval '1 minute');
ROLLBACK;
SQL

role_sql "$execution_url" robin_execution_live_control >/dev/null <<'SQL'
BEGIN;
INSERT INTO execution_market_review_records (review_record_sha256, record)
VALUES (repeat('a', 64), '{"source":"authority-test"}'::jsonb);
INSERT INTO execution_market_review_observations (
    review_record_sha256, source, response_sha256, observed_at
) VALUES (
    repeat('a', 64),
    'https://mainnet.zklighter.elliot.ai/api/v1/orderBooks',
    repeat('b', 64),
    now()
);
INSERT INTO execution_market_configs (
    manifest_id, symbol, spot_token, lighter_market_index, spot_decimals,
    perp_base_decimals, perp_price_decimals, spot_config_version,
    ui_multiplier_e18, max_price_deviation_bps, max_spot_slippage_bps,
    max_unwind_price_deviation_bps, review_record_sha256, valid_from, valid_until
) VALUES (
    '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
    'AAPL',
    '0x1111111111111111111111111111111111111111',
    1,
    18,
    8,
    6,
    1,
    '1000000000000000000',
    100,
    100,
    500,
    repeat('a', 64),
    now(),
    now() + interval '1 day'
);
INSERT INTO live_scheduler_approvals (
    evaluation_id,
    execution_account_id,
    agent_id,
    evaluation,
    readiness,
    account_state,
    approval_sha256,
    expires_at,
    approval_version
) VALUES (
    '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    'singleton-mainnet-canary',
    'singleton-mainnet-canary',
    jsonb_build_object(
        'id', '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
        'strategy_version', 'basis-aapl-v1',
        'strategy_manifest_sha256', 'c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32',
        'source_config_sha256', '59106a18758a95af45e6ac1a8257843cfbd2a45fd09b5b3c3f429d3dedb56c2a',
        'dataset_manifest', 'authority-dataset',
        'market_manifest', '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
        'status', 'approved',
        'action', 'entry',
        'observed_at_ms', 1,
        'estimated_cost_micros', 1,
        'source_episode_id', '30000000-0000-4000-8000-000000000001',
        'paper_evaluation_id', '30000000-0000-4000-8000-000000000002',
        'pair_intent_id', ''
    ),
    jsonb_build_object(
        'execution_account_id', 'singleton-mainnet-canary',
        'agent_id', 'singleton-mainnet-canary',
        'strategy_version', 'basis-aapl-v1',
        'strategy_manifest_sha256', 'c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32',
        'lifecycle', 'ready',
        'global_control', 'ACTIVE',
        'strategy_control', 'ACTIVE',
        'account_control', 'ACTIVE',
        'fully_verified', true,
        'vault_wired', true,
        'vault_funded', true,
        'execution_signer_funded', true,
        'lighter_linked', true,
        'lighter_funded', true,
        'route_healthy', true,
        'oracle_healthy', true,
        'sequencer_healthy', true,
        'observed_at_ms', 1
    ),
    jsonb_build_object(
        'execution_account_id', 'singleton-mainnet-canary',
        'agent_id', 'singleton-mainnet-canary',
        'strategy_manifest_sha256', 'c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32',
        'lighter_account_index', 1,
        'lighter_api_key_index', 4,
        'lighter_market_index', 1,
        'lighter_nonce_aligned', true,
        'unknown_lighter_orders', false,
        'unknown_lighter_positions', false,
        'collateral_micros', 100000000,
        'maintenance_margin_micros', 1000000,
        'robinhood_vault', '0x1111111111111111111111111111111111111111',
        'robinhood_signer', '0x2222222222222222222222222222222222222222',
        'robinhood_nonce_aligned', true,
        'unknown_robinhood_position', false,
        'nav_micros', 100000000,
        'daily_turnover_micros', 0,
        'active_episodes', 0,
        'flat', true,
        'spot_decimals', 18,
        'spot_config_version', 1,
        'ui_multiplier_e18', '1000000000000000000',
        'next_client_order_index', 1,
        'next_unwind_order_index', 2,
        'observed_at_ms', 1
    ),
    repeat('a', 64),
    now() + interval '1 minute',
    2
);
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM live_scheduler_work
        WHERE evaluation_id =
            '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
          AND execution_account_id = 'singleton-mainnet-canary'
    ) THEN
        RAISE EXCEPTION 'scheduler work trigger did not execute';
    END IF;
END;
$$;
ROLLBACK;
SQL

for index in 1 2 3; do
  role_sql "$execution_url" "robin_execution_sequencer_${index}" >/dev/null <<SQL
INSERT INTO sequencer_publisher_state (
    publisher_id, chain_id, feed_address, signer_address
) VALUES (
    'sequencer-publisher-${index}',
    4663,
    '0x1111111111111111111111111111111111111111',
    '0x000000000000000000000000000000000000000${index}'
);
INSERT INTO sequencer_publisher_transactions (
    publisher_id, sequence, nonce, tx_hash, raw_transaction, healthy,
    started_at, status, created_at
) VALUES (
    'sequencer-publisher-${index}',
    1,
    ${index},
    'sequencer-authority-${index}',
    decode('00', 'hex'),
    true,
    1,
    'confirmed',
    now()
);
SQL
  count="$(
    role_sql "$execution_url" "robin_execution_sequencer_${index}" -Atc \
      "SELECT count(*) FROM sequencer_publisher_state"
  )"
  [[ "$count" == "1" ]] || {
    echo "sequencer publisher ${index} can observe another publisher" >&2
    exit 1
  }
done
expect_role_failure "$execution_url" robin_execution_sequencer_1 \
  "violates row-level security policy" \
  -c "INSERT INTO sequencer_publisher_state (
        publisher_id, chain_id, feed_address, signer_address
      ) VALUES (
        'sequencer-publisher-2', 4663,
        '0x1111111111111111111111111111111111111111',
        '0x9999999999999999999999999999999999999999'
      )"
role_sql "$execution_url" robin_execution_sequencer_1 -c \
  "UPDATE sequencer_publisher_state
   SET quarantined_reason = 'cross-tenant-write'
   WHERE publisher_id = 'sequencer-publisher-2'" >/dev/null
expect_role_failure "$execution_url" robin_execution_sequencer_1 "permission denied" \
  -c "DELETE FROM sequencer_publisher_transactions
      WHERE publisher_id = 'sequencer-publisher-2'"
sequencer_cross_write="$(
  owner_sql "$execution_url" -Atc "
    SELECT count(*) FILTER (WHERE quarantined_reason = 'cross-tenant-write')
           || ':' || count(*)
    FROM sequencer_publisher_state state
    LEFT JOIN sequencer_publisher_transactions tx
      USING (publisher_id)
    WHERE state.publisher_id = 'sequencer-publisher-2'
  "
)"
[[ "$sequencer_cross_write" == "0:1" ]] || {
  echo "sequencer publisher crossed its row-level boundary" >&2
  exit 1
}

for index in 1 2 3; do
  role_sql "$execution_url" "robin_execution_aapl_relay_${index}" >/dev/null <<SQL
INSERT INTO aapl_relay_state (
    publisher_id, source_chain_id, source_feed, source_code_hash,
    target_chain_id, target_feed, target_code_hash, signer_address
) VALUES (
    'aapl-relay-${index}',
    1,
    '0x1111111111111111111111111111111111111111',
    '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    4663,
    '0x2222222222222222222222222222222222222222',
    '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
    '0x000000000000000000000000000000000000000${index}'
);
INSERT INTO aapl_relay_transactions (
    publisher_id, sequence, nonce, tx_hash, raw_transaction, source_round,
    source_answer, source_updated_at, answered_in_round, status, created_at
) VALUES (
    'aapl-relay-${index}',
    1,
    ${index},
    'aapl-authority-${index}',
    decode('00', 'hex'),
    1,
    1,
    1,
    1,
    'confirmed',
    now()
);
SQL
  count="$(
    role_sql "$execution_url" "robin_execution_aapl_relay_${index}" -Atc \
      "SELECT count(*) FROM aapl_relay_state"
  )"
  [[ "$count" == "1" ]] || {
    echo "AAPL relay ${index} can observe another publisher" >&2
    exit 1
  }
done
expect_role_failure "$execution_url" robin_execution_aapl_relay_1 \
  "violates row-level security policy" \
  -c "INSERT INTO aapl_relay_state (
        publisher_id, source_chain_id, source_feed, source_code_hash,
        target_chain_id, target_feed, target_code_hash, signer_address
      ) VALUES (
        'aapl-relay-2', 1,
        '0x1111111111111111111111111111111111111111',
        '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
        4663,
        '0x2222222222222222222222222222222222222222',
        '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
        '0x9999999999999999999999999999999999999999'
      )"
role_sql "$execution_url" robin_execution_aapl_relay_1 -c \
  "UPDATE aapl_relay_state
   SET quarantined_reason = 'cross-tenant-write'
   WHERE publisher_id = 'aapl-relay-2'" >/dev/null
expect_role_failure "$execution_url" robin_execution_aapl_relay_1 "permission denied" \
  -c "DELETE FROM aapl_relay_transactions
      WHERE publisher_id = 'aapl-relay-2'"
aapl_cross_write="$(
  owner_sql "$execution_url" -Atc "
    SELECT count(*) FILTER (WHERE quarantined_reason = 'cross-tenant-write')
           || ':' || count(*)
    FROM aapl_relay_state state
    LEFT JOIN aapl_relay_transactions tx
      USING (publisher_id)
    WHERE state.publisher_id = 'aapl-relay-2'
  "
)"
[[ "$aapl_cross_write" == "0:1" ]] || {
  echo "AAPL relay crossed its row-level boundary" >&2
  exit 1
}
role_sql "$execution_url" robin_execution_readonly -Atc \
  "SELECT count(*) FROM execution_accounts" >/dev/null
publisher_rows="$(
  role_sql "$execution_url" robin_execution_readonly -Atc \
    "SELECT (SELECT count(*) FROM sequencer_publisher_state)
            || ':' ||
            (SELECT count(*) FROM aapl_relay_state)"
)"
[[ "$publisher_rows" == "3:3" ]] || {
  echo "execution evidence role cannot observe all publisher journals" >&2
  exit 1
}

role_sql "$lighter_url" robin_lighter_provisioner >/dev/null <<'SQL'
BEGIN;
INSERT INTO lighter_provisioner_request_nonces (caller, nonce, expires_at)
VALUES ('authority-test', 'authority-nonce', now() + interval '1 minute');
ROLLBACK;
SQL
role_sql "$lighter_url" robin_lighter_readonly -Atc \
  "SELECT count(*) FROM lighter_signing_requests" >/dev/null

role_sql "$custody_url" robin_custody_provisioner >/dev/null <<'SQL'
BEGIN;
INSERT INTO robinhood_provisioner_auth_nonces (caller_id, nonce, expires_at)
VALUES ('authority-test', 'authority-nonce', now() + interval '1 minute');
ROLLBACK;
SQL
role_sql "$custody_url" robin_custody_signer >/dev/null <<'SQL'
BEGIN;
INSERT INTO robinhood_signer_deployments (
    deployment_id, manifest, chain_id, signer_address, vault_address
) VALUES (
    repeat('a', 64),
    '{"source":"authority-test"}'::jsonb,
    4663,
    '0x1111111111111111111111111111111111111111',
    '0x2222222222222222222222222222222222222222'
);
ROLLBACK;
SQL
role_sql "$custody_url" robin_custody_readonly -Atc \
  "SELECT count(*) FROM robinhood_execution_bindings" >/dev/null

owner_sql "$execution_url" >/dev/null <<'SQL'
INSERT INTO execution_intents (
    id, strategy_version, symbol, direction, payload, saga, active,
    execution_account_id, agent_id, source_evaluation_id, risk_version,
    payload_sha256
) VALUES (
    '0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd',
    'basis-aapl-v1',
    'AAPL',
    'long_spot_short_perp',
    '{}'::jsonb,
    '{}'::jsonb,
    true,
    'singleton-mainnet-canary',
    'singleton-mainnet-canary',
    'authority-evaluation',
    'basis-aapl-v1-risk',
    repeat('d', 64)
);
SQL
expect_lock_failure "execution episodes must be flat before migration release"
owner_sql "$execution_url" -c "
  UPDATE execution_intents
  SET active = false
  WHERE id = '0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd'
" >/dev/null

owner_sql "$execution_url" >/dev/null <<'SQL'
INSERT INTO execution_accounts (
    execution_account_id, agent_id, strategy_version, risk_version, status,
    lighter_account_index, lighter_api_key_index, robinhood_vault,
    robinhood_signer, binding_sha256, owner_address, strategy_manifest_sha256
) VALUES (
    'authority-test-account',
    'authority-test-agent',
    'basis-aapl-v1',
    'basis-aapl-v1-risk',
    'active',
    901,
    4,
    '0x1111111111111111111111111111111111111111',
    '0x2222222222222222222222222222222222222222',
    repeat('1', 64),
    '0x3333333333333333333333333333333333333333',
    'c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32'
);
INSERT INTO execution_account_control (execution_account_id, mode, reason)
VALUES ('authority-test-account', 'HALTED', 'authority test');
INSERT INTO execution_account_registrations (
    execution_account_id, agent_id, strategy_version, risk_version,
    strategy_manifest_sha256, lighter_account_index, lighter_api_key_index,
    robinhood_owner, robinhood_vault, robinhood_signer, binding_sha256
) VALUES (
    'authority-test-account',
    'authority-test-agent',
    'basis-aapl-v1',
    'basis-aapl-v1-risk',
    'c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32',
    901,
    4,
    '0x3333333333333333333333333333333333333333',
    '0x1111111111111111111111111111111111111111',
    '0x2222222222222222222222222222222222222222',
    repeat('1', 64)
);
SQL
expect_lock_failure \
  "registered accounts require fresh authoritative flat snapshots before migration release"

owner_sql "$execution_url" >/dev/null <<'SQL'
INSERT INTO execution_account_snapshots (
    execution_account_id, source, source_session, source_sequence, payload,
    payload_sha256, observed_at, received_at, expires_at
) VALUES
(
    'authority-test-account',
    'lighter-auth',
    'authority-stale',
    999,
    jsonb_build_object(
        'account_index', 901,
        'api_key_index', 4,
        'market_index', 1,
        'nonce_aligned', true,
        'no_unknown_orders', true,
        'no_unknown_positions', true,
        'collateral_ready', true,
        'maintenance_margin_ratio_micros', 2000000,
        'collateral_micros', 100000000,
        'maintenance_margin_micros', 1000000,
        'flat', true
    ),
    repeat('4', 64),
    now() - interval '2 minutes',
    now() - interval '2 minutes',
    now() - interval '1 minute'
),
(
    'authority-test-account',
    'robinhood-chain',
    'authority-stale',
    999,
    jsonb_build_object(
        'vault_address', '0x1111111111111111111111111111111111111111',
        'signer_address', '0x2222222222222222222222222222222222222222',
        'funding_ready', true,
        'wiring_verified', true,
        'finality_healthy', true,
        'flat', true,
        'owner_address', '0x3333333333333333333333333333333333333333',
        'agent_enabled', true,
        'risk_mode', 'ACTIVE',
        'finalized_agent_address', '0x2222222222222222222222222222222222222222',
        'finalized_agent_enabled', true,
        'finalized_agent_revoked', false,
        'global_mode', 'ACTIVE',
        'finalized_global_mode', 'ACTIVE',
        'finalized_risk_mode', 'ACTIVE',
        'settlement_balance_raw', '100000000',
        'nonce_aligned', true,
        'spot_config_version', 1,
        'stock_decimals', 18,
        'ui_multiplier_e18', '1000000000000000000',
        'new_ui_multiplier_e18', '1000000000000000000',
        'oracle_paused', false,
        'oracle_healthy', true,
        'sequencer_healthy', true,
        'signer_gas_ready', true,
        'finalized_number', 100,
        'finalized_hash', '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
        'finalized_timestamp', 1,
        'source_block_number', 100,
        'source_block_hash', '0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
        'source_block_timestamp', 1
    ),
    repeat('5', 64),
    now() - interval '2 minutes',
    now() - interval '2 minutes',
    now() - interval '1 minute'
);
SQL
expect_lock_failure \
  "registered accounts require fresh authoritative flat snapshots before migration release"

owner_sql "$execution_url" >/dev/null <<'SQL'
INSERT INTO execution_account_snapshots (
    execution_account_id, source, source_session, source_sequence, payload,
    payload_sha256, observed_at, received_at, expires_at
) VALUES
(
    'authority-test-account',
    'lighter-auth',
    'authority-unknown',
    2,
    jsonb_build_object(
        'account_index', 901,
        'api_key_index', 4,
        'market_index', 1,
        'nonce_aligned', true,
        'no_unknown_orders', false,
        'no_unknown_positions', true,
        'collateral_ready', true,
        'maintenance_margin_ratio_micros', 2000000,
        'collateral_micros', 100000000,
        'maintenance_margin_micros', 1000000,
        'flat', false
    ),
    repeat('6', 64),
    now(),
    now(),
    now() + interval '1 minute'
),
(
    'authority-test-account',
    'robinhood-chain',
    'authority-unknown',
    2,
    jsonb_build_object(
        'vault_address', '0x1111111111111111111111111111111111111111',
        'signer_address', '0x2222222222222222222222222222222222222222',
        'funding_ready', true,
        'wiring_verified', true,
        'finality_healthy', false,
        'flat', false,
        'owner_address', '0x3333333333333333333333333333333333333333',
        'agent_enabled', true,
        'risk_mode', 'ACTIVE',
        'finalized_agent_address', '0x2222222222222222222222222222222222222222',
        'finalized_agent_enabled', true,
        'finalized_agent_revoked', false,
        'global_mode', 'ACTIVE',
        'finalized_global_mode', 'ACTIVE',
        'finalized_risk_mode', 'ACTIVE',
        'settlement_balance_raw', '100000000',
        'nonce_aligned', true,
        'spot_config_version', 1,
        'stock_decimals', 18,
        'ui_multiplier_e18', '1000000000000000000',
        'new_ui_multiplier_e18', '1000000000000000000',
        'oracle_paused', false,
        'oracle_healthy', true,
        'sequencer_healthy', true,
        'signer_gas_ready', true,
        'finalized_number', 101,
        'finalized_hash', '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
        'finalized_timestamp', 2,
        'source_block_number', 101,
        'source_block_hash', '0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
        'source_block_timestamp', 2
    ),
    repeat('7', 64),
    now(),
    now(),
    now() + interval '1 minute'
);
SQL
expect_lock_failure \
  "registered accounts require fresh authoritative flat snapshots before migration release"

owner_sql "$execution_url" >/dev/null <<'SQL'
INSERT INTO execution_account_snapshots (
    execution_account_id, source, source_session, source_sequence, payload,
    payload_sha256, observed_at, received_at, expires_at
)
SELECT
    execution_account_id,
    source,
    'authority-identity-mismatch',
    3,
    CASE source
        WHEN 'lighter-auth' THEN payload || jsonb_build_object(
            'account_index', 902,
            'no_unknown_orders', true,
            'flat', true
        )
        ELSE payload || jsonb_build_object(
            'signer_address', '0x4444444444444444444444444444444444444444',
            'finality_healthy', true,
            'flat', true
        )
    END,
    CASE source WHEN 'lighter-auth' THEN repeat('a', 64) ELSE repeat('b', 64) END,
    now(),
    now(),
    now() + interval '5 seconds'
FROM execution_account_snapshots
WHERE execution_account_id = 'authority-test-account'
  AND source_session = 'authority-unknown';
SQL
expect_lock_failure \
  "registered accounts require fresh authoritative flat snapshots before migration release"

owner_sql "$execution_url" >/dev/null <<'SQL'
INSERT INTO execution_account_snapshots (
    execution_account_id, source, source_session, source_sequence, payload,
    payload_sha256, observed_at, received_at, expires_at
) VALUES
(
    'authority-test-account',
    'lighter-auth',
    'authority-flat',
    3,
    jsonb_build_object(
        'account_index', 901,
        'api_key_index', 4,
        'market_index', 1,
        'nonce_aligned', true,
        'no_unknown_orders', true,
        'no_unknown_positions', true,
        'collateral_ready', true,
        'maintenance_margin_ratio_micros', 2000000,
        'collateral_micros', 100000000,
        'maintenance_margin_micros', 1000000,
        'flat', true
    ),
    repeat('8', 64),
    now(),
    now(),
    now() + interval '5 seconds'
),
(
    'authority-test-account',
    'robinhood-chain',
    'authority-flat',
    3,
    jsonb_build_object(
        'vault_address', '0x1111111111111111111111111111111111111111',
        'signer_address', '0x2222222222222222222222222222222222222222',
        'funding_ready', true,
        'wiring_verified', true,
        'finality_healthy', true,
        'flat', true,
        'owner_address', '0x3333333333333333333333333333333333333333',
        'agent_enabled', true,
        'risk_mode', 'ACTIVE',
        'finalized_agent_address', '0x2222222222222222222222222222222222222222',
        'finalized_agent_enabled', true,
        'finalized_agent_revoked', false,
        'global_mode', 'ACTIVE',
        'finalized_global_mode', 'ACTIVE',
        'finalized_risk_mode', 'ACTIVE',
        'settlement_balance_raw', '100000000',
        'nonce_aligned', true,
        'spot_config_version', 1,
        'stock_decimals', 18,
        'ui_multiplier_e18', '1000000000000000000',
        'new_ui_multiplier_e18', '1000000000000000000',
        'oracle_paused', false,
        'oracle_healthy', true,
        'sequencer_healthy', true,
        'signer_gas_ready', true,
        'finalized_number', 102,
        'finalized_hash', '0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc',
        'finalized_timestamp', 3,
        'source_block_number', 102,
        'source_block_hash', '0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc',
        'source_block_timestamp', 3
    ),
    repeat('9', 64),
    now(),
    now(),
    now() + interval '5 seconds'
);
SQL
owner_sql "$execution_url" >/dev/null <<'SQL'
UPDATE execution_control SET mode = 'ACTIVE', reason = 'authority test' WHERE singleton;
UPDATE execution_strategy_control SET mode = 'ACTIVE', reason = 'authority test';
UPDATE execution_account_control
SET mode = 'ACTIVE',
    reason = CASE
        WHEN execution_account_id = 'authority-test-account'
            THEN 'strategy release changed; reconcile and reprovision'
        ELSE 'authority test'
    END;
UPDATE execution_rollout_readiness
SET alerting_ready = TRUE, safe_rotation_ready = TRUE
WHERE singleton;
UPDATE execution_account_readiness
SET venue_approved = TRUE,
    oracle_healthy = TRUE,
    sequencer_healthy = TRUE,
    reconciliation_ready = TRUE,
    exit_authority_ready = TRUE;
SQL
owner_sql "$execution_url" --single-transaction \
  --file scripts/lock-execution-after-migration.sql >/dev/null
release_state="$(
  owner_sql "$execution_url" -Atc "
    SELECT
      (SELECT count(*) FROM execution_control WHERE mode <> 'HALTED')
      || ':' ||
      (SELECT count(*) FROM execution_strategy_control WHERE mode <> 'HALTED')
      || ':' ||
      (SELECT count(*) FROM execution_account_control WHERE mode <> 'HALTED')
      || ':' ||
      (SELECT count(*) FROM execution_rollout_readiness
       WHERE alerting_ready OR safe_rotation_ready)
      || ':' ||
      (SELECT count(*) FROM execution_account_readiness
       WHERE venue_approved OR oracle_healthy OR sequencer_healthy
          OR reconciliation_ready OR exit_authority_ready)
      || ':' ||
      (SELECT count(*) FROM execution_account_control
       WHERE reason = 'strategy release changed; reconcile and reprovision')
  "
)"
[[ "$release_state" == "0:0:0:0:0:1" ]] || {
  echo "migration release left stale controls or readiness active" >&2
  exit 1
}

echo "database authority integration: ok"
