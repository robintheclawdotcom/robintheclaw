#!/usr/bin/env bash
set -euo pipefail

database_url="${1:-}"
role="${2:-}"

if [[ -z "$database_url" ]]; then
  echo "database owner URL is required" >&2
  exit 1
fi
if [[ -z "${ROBIN_DATABASE_PASSWORD:-}" || ${#ROBIN_DATABASE_PASSWORD} -lt 32 ]]; then
  echo "database role password must contain at least 32 characters" >&2
  exit 1
fi

readonly="false"
direct_function=""
deprecated_role=""
delete_pattern='a^'
case "$role" in
  robin_app_api)
    read_pattern='.*'
    write_pattern='.*'
    delete_pattern='^(wallet_links|app_internal_nonces)$'
    ;;
  robin_app_paper)
    read_pattern='^(agents|agent_paper_events)$'
    write_pattern='^agent_paper_events$'
    ;;
  robin_app_readonly)
    readonly="true"
    read_pattern='.*'
    write_pattern='a^'
    ;;
  robin_research_collector)
    read_pattern='^(raw_market_events|market_features|source_health|event_staging|archive_segments|archive_segment_events|archive_manifests)$'
    write_pattern="$read_pattern"
    delete_pattern='^event_staging$'
    direct_function='ensure_event_staging_partition(timestamp with time zone)'
    ;;
  robin_research_paper)
    read_pattern='^(raw_market_events|paper_agent_cursors|paper_evaluations|paper_market_state|paper_opportunity_episodes|agent_fanout_outbox)$'
    write_pattern='^(paper_agent_cursors|paper_evaluations|paper_market_state|paper_opportunity_episodes|agent_fanout_outbox)$'
    ;;
  robin_research_readonly)
    readonly="true"
    read_pattern='.*'
    write_pattern='a^'
    ;;
  robin_execution_coordinator)
    read_pattern='^(execution_.*|live_.*)$'
    write_pattern='^execution_.*'
    delete_pattern='^execution_api_nonces$'
    ;;
  robin_execution_live_control)
    read_pattern='^(execution_.*|live_.*)$'
    write_pattern='^(live_.*|execution_market_(configs|review_records|review_observations))$'
    ;;
  robin_execution_sequencer_1|robin_execution_sequencer_2|robin_execution_sequencer_3)
    read_pattern='^sequencer_publisher_.*$'
    write_pattern="$read_pattern"
    deprecated_role='robin_execution_sequencer'
    ;;
  robin_execution_aapl_relay_1|robin_execution_aapl_relay_2|robin_execution_aapl_relay_3)
    read_pattern='^aapl_relay_.*$'
    write_pattern="$read_pattern"
    deprecated_role='robin_execution_aapl_relay'
    ;;
  robin_execution_readonly)
    readonly="true"
    read_pattern='.*'
    write_pattern='a^'
    ;;
  robin_lighter_provisioner)
    read_pattern='^lighter_.*$'
    write_pattern="$read_pattern"
    delete_pattern='^lighter_provisioner_request_nonces$'
    ;;
  robin_lighter_readonly)
    readonly="true"
    read_pattern='^lighter_signing_requests$'
    write_pattern='a^'
    ;;
  robin_custody_provisioner)
    read_pattern='^robinhood_(execution_bindings|provisioner_auth_nonces|provisioner_audit)$'
    write_pattern="$read_pattern"
    delete_pattern='^robinhood_provisioner_auth_nonces$'
    ;;
  robin_custody_signer)
    read_pattern='^robinhood_signer_.*$'
    write_pattern="$read_pattern"
    delete_pattern='^robinhood_signer_auth_nonces$'
    ;;
  robin_custody_readonly)
    readonly="true"
    read_pattern='^robinhood_.*$'
    write_pattern='a^'
    ;;
  *)
    echo "database role is not part of the reviewed policy" >&2
    exit 1
    ;;
esac

ROBIN_DATABASE_PASSWORD="$ROBIN_DATABASE_PASSWORD" \
ROBIN_DATABASE_URL="$database_url" ruby scripts/psql-with-url.rb \
  --set ON_ERROR_STOP=1 \
  --set role="$role" \
  --set readonly="$readonly" \
  --set read_pattern="$read_pattern" \
  --set write_pattern="$write_pattern" \
  --set delete_pattern="$delete_pattern" \
  --set direct_function="$direct_function" \
  --set deprecated_role="$deprecated_role" <<'SQL'
\getenv role_password ROBIN_DATABASE_PASSWORD

SELECT format(
    'DO $guard$ BEGIN RAISE EXCEPTION %L; END $guard$',
    'database owner cannot provision reviewed runtime roles'
)
WHERE NOT EXISTS (
    SELECT 1
    FROM pg_roles
    WHERE rolname = current_user
      AND (rolsuper OR rolcreaterole)
)
\gexec

SELECT format('ALTER ROLE %I NOLOGIN PASSWORD NULL', :'deprecated_role')
WHERE :'deprecated_role' <> ''
  AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'deprecated_role')
\gexec
SELECT format('REVOKE ALL ON DATABASE %I FROM %I', current_database(), :'deprecated_role')
WHERE :'deprecated_role' <> ''
  AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'deprecated_role')
\gexec
SELECT format('REVOKE ALL ON SCHEMA public FROM %I', :'deprecated_role')
WHERE :'deprecated_role' <> ''
  AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'deprecated_role')
\gexec
SELECT format(
    'REVOKE ALL ON %s %I.%I FROM %I',
    CASE WHEN class.relkind = 'S' THEN 'SEQUENCE' ELSE 'TABLE' END,
    namespace.nspname,
    class.relname,
    :'deprecated_role'
)
FROM pg_class class
JOIN pg_namespace namespace ON namespace.oid = class.relnamespace
WHERE :'deprecated_role' <> ''
  AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'deprecated_role')
  AND namespace.nspname = 'public'
  AND class.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')
\gexec
SELECT format('REVOKE EXECUTE ON FUNCTION %s FROM %I', routine.oid::regprocedure, :'deprecated_role')
FROM pg_proc routine
JOIN pg_namespace namespace ON namespace.oid = routine.pronamespace
WHERE :'deprecated_role' <> ''
  AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'deprecated_role')
  AND namespace.nspname = 'public'
\gexec

SELECT format(
    'CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS',
    :'role',
    :'role_password'
)
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'role')
\gexec

SELECT format(
    'ALTER ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS',
    :'role',
    :'role_password'
)
\gexec

SELECT format('REVOKE %I FROM %I', parent.rolname, member.rolname)
FROM pg_auth_members membership
JOIN pg_roles parent ON parent.oid = membership.roleid
JOIN pg_roles member ON member.oid = membership.member
WHERE member.rolname = :'role'
\gexec

SELECT format('REVOKE ALL ON DATABASE %I FROM %I', current_database(), :'role')
\gexec
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', current_database())
\gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO %I', current_database(), :'role')
\gexec

REVOKE ALL ON SCHEMA public FROM PUBLIC;
SELECT format('REVOKE ALL ON SCHEMA public FROM %I', :'role')
\gexec
SELECT format('GRANT USAGE ON SCHEMA public TO %I', :'role')
\gexec

SELECT format('REVOKE ALL ON %s %I.%I FROM %I',
              CASE WHEN class.relkind IN ('S') THEN 'SEQUENCE' ELSE 'TABLE' END,
              namespace.nspname, class.relname, :'role')
FROM pg_class class
JOIN pg_namespace namespace ON namespace.oid = class.relnamespace
WHERE namespace.nspname = 'public'
  AND class.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')
\gexec

REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM PUBLIC;
SELECT format('REVOKE EXECUTE ON FUNCTION %s FROM %I', routine.oid::regprocedure, :'role')
FROM pg_proc routine
JOIN pg_namespace namespace ON namespace.oid = routine.pronamespace
WHERE namespace.nspname = 'public'
\gexec
SELECT format(
    'ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC',
    current_user
)
\gexec

SELECT format('GRANT SELECT ON TABLE %I.%I TO %I', namespace.nspname, class.relname, :'role')
FROM pg_class class
JOIN pg_namespace namespace ON namespace.oid = class.relnamespace
WHERE namespace.nspname = 'public'
  AND class.relkind IN ('r', 'p', 'v', 'm', 'f')
  AND class.relname ~ :'read_pattern'
  AND class.relname !~ 'schema_migrations$'
  AND class.relname <> '_sqlx_migrations'
\gexec

SELECT format(
    'GRANT SELECT, INSERT, UPDATE ON TABLE %I.%I TO %I',
    namespace.nspname,
    class.relname,
    :'role'
)
FROM pg_class class
JOIN pg_namespace namespace ON namespace.oid = class.relnamespace
WHERE namespace.nspname = 'public'
  AND class.relkind IN ('r', 'p')
  AND class.relname ~ :'write_pattern'
  AND class.relname !~ 'schema_migrations$'
  AND class.relname <> '_sqlx_migrations'
\gexec

SELECT format(
    'GRANT DELETE ON TABLE %I.%I TO %I',
    namespace.nspname,
    class.relname,
    :'role'
)
FROM pg_class class
JOIN pg_namespace namespace ON namespace.oid = class.relnamespace
WHERE namespace.nspname = 'public'
  AND class.relkind IN ('r', 'p')
  AND class.relname ~ :'delete_pattern'
  AND class.relname !~ 'schema_migrations$'
  AND class.relname <> '_sqlx_migrations'
\gexec

SELECT DISTINCT format(
    'GRANT USAGE, SELECT, UPDATE ON SEQUENCE %I.%I TO %I',
    sequence_namespace.nspname,
    sequence.relname,
    :'role'
)
FROM pg_class sequence
JOIN pg_namespace sequence_namespace ON sequence_namespace.oid = sequence.relnamespace
JOIN pg_depend dependency ON dependency.objid = sequence.oid
JOIN pg_class table_class ON table_class.oid = dependency.refobjid
JOIN pg_namespace table_namespace ON table_namespace.oid = table_class.relnamespace
WHERE sequence.relkind = 'S'
  AND sequence_namespace.nspname = 'public'
  AND table_namespace.nspname = 'public'
  AND table_class.relname ~ :'write_pattern'
  AND table_class.relname !~ 'schema_migrations$'
\gexec

SELECT DISTINCT format(
    'GRANT EXECUTE ON FUNCTION %s TO %I',
    routine.oid::regprocedure,
    :'role'
)
FROM (
    SELECT dependency.refobjid AS routine_oid
    FROM pg_constraint constraint_record
    JOIN pg_class table_class ON table_class.oid = constraint_record.conrelid
    JOIN pg_namespace table_namespace ON table_namespace.oid = table_class.relnamespace
    JOIN pg_depend dependency
      ON dependency.classid = 'pg_constraint'::regclass
     AND dependency.objid = constraint_record.oid
     AND dependency.refclassid = 'pg_proc'::regclass
    WHERE table_namespace.nspname = 'public'
      AND table_class.relname ~ :'write_pattern'

    UNION

    SELECT trigger_record.tgfoid
    FROM pg_trigger trigger_record
    JOIN pg_class table_class ON table_class.oid = trigger_record.tgrelid
    JOIN pg_namespace table_namespace ON table_namespace.oid = table_class.relnamespace
    WHERE table_namespace.nspname = 'public'
      AND table_class.relname ~ :'write_pattern'
      AND NOT trigger_record.tgisinternal
) required
JOIN pg_proc routine ON routine.oid = required.routine_oid
JOIN pg_namespace routine_namespace ON routine_namespace.oid = routine.pronamespace
WHERE routine_namespace.nspname = 'public'
  AND NOT routine.prosecdef
\gexec

SELECT format(
    'DO $guard$ BEGIN RAISE EXCEPTION %L; END $guard$',
    'reviewed database function is missing or unsafe'
)
WHERE :'direct_function' <> ''
  AND NOT EXISTS (
      SELECT 1
      FROM pg_proc routine
      JOIN pg_namespace namespace ON namespace.oid = routine.pronamespace
      WHERE namespace.nspname = 'public'
        AND routine.oid::regprocedure::text = :'direct_function'
        AND routine.prosecdef
        AND routine.proconfig = ARRAY['search_path=pg_catalog']
  )
\gexec

SELECT format('GRANT EXECUTE ON FUNCTION %s TO %I', routine.oid::regprocedure, :'role')
FROM pg_proc routine
JOIN pg_namespace namespace ON namespace.oid = routine.pronamespace
WHERE :'direct_function' <> ''
  AND namespace.nspname = 'public'
  AND routine.oid::regprocedure::text = :'direct_function'
  AND routine.prosecdef
  AND routine.proconfig = ARRAY['search_path=pg_catalog']
\gexec

SELECT format(
    'ALTER ROLE %I SET default_transaction_read_only = %s',
    :'role',
    CASE WHEN :'readonly'::boolean THEN 'on' ELSE 'off' END
)
\gexec
SELECT format('ALTER ROLE %I SET search_path = public', :'role')
\gexec
SELECT format('ALTER ROLE %I SET statement_timeout = %L', :'role', '30s')
\gexec

SELECT format(
    'DO $guard$ BEGIN RAISE EXCEPTION %L; END $guard$',
    'database role has elevated authority or inherited membership'
)
WHERE EXISTS (
    SELECT 1
    FROM pg_roles role_record
    WHERE role_record.rolname = :'role'
      AND (
          role_record.rolsuper
          OR role_record.rolcreatedb
          OR role_record.rolcreaterole
          OR role_record.rolreplication
          OR role_record.rolbypassrls
          OR EXISTS (
              SELECT 1 FROM pg_auth_members membership
              WHERE membership.member = role_record.oid
          )
      )
)
\gexec
SQL
