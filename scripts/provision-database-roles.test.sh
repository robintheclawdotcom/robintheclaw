#!/usr/bin/env bash
set -euo pipefail

database_url="${DATABASE_ROLE_TEST_URL:-}"
if [[ -z "$database_url" ]]; then
  echo "DATABASE_ROLE_TEST_URL is required" >&2
  exit 1
fi

password="database-role-password-000000000000000000000000"

ROBIN_DATABASE_URL="$database_url" ruby scripts/psql-with-url.rb --set ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE users (id bigint PRIMARY KEY);
CREATE TABLE agent_paper_events (id bigint PRIMARY KEY);
CREATE TABLE execution_control (id bigint PRIMARY KEY, value text NOT NULL);
CREATE TABLE live_scheduler_work (id bigint PRIMARY KEY, value text NOT NULL);
CREATE TABLE robinhood_execution_bindings (id bigint PRIMARY KEY, value text NOT NULL);
CREATE TABLE robinhood_signer_transactions (id bigint PRIMARY KEY, value text NOT NULL);
CREATE FUNCTION forbidden_function() RETURNS bigint
LANGUAGE sql
AS 'SELECT 1';
INSERT INTO execution_control VALUES (1, 'halted');
INSERT INTO live_scheduler_work VALUES (1, 'pending');
INSERT INTO robinhood_execution_bindings VALUES (1, 'binding');
INSERT INTO robinhood_signer_transactions VALUES (1, 'signed');
SQL

provision() {
  ROBIN_DATABASE_PASSWORD="$password" \
    bash scripts/provision-database-roles.sh "$database_url" "$1"
}

role_url() {
  DATABASE_OWNER_URL="$database_url" DATABASE_PASSWORD="$password" ROLE="$1" \
    ruby -I scripts -r database-runtime-exec -e \
      'puts DatabaseRuntime.runtime_url(ENV.fetch("DATABASE_OWNER_URL"), ENV.fetch("ROLE"), ENV.fetch("DATABASE_PASSWORD"))'
}

provision robin_app_readonly
provision robin_execution_live_control
provision robin_custody_provisioner
provision robin_custody_signer

readonly_url="$(role_url robin_app_readonly)"
live_control_url="$(role_url robin_execution_live_control)"
provisioner_url="$(role_url robin_custody_provisioner)"
signer_url="$(role_url robin_custody_signer)"

if output="$(
  ROBIN_DATABASE_PASSWORD="$password" \
    bash scripts/provision-database-roles.sh "$readonly_url" robin_app_paper 2>&1
)"; then
  echo "runtime role provisioned another database role" >&2
  exit 1
fi
grep -Fq "database owner cannot provision reviewed runtime roles" <<<"$output" || {
  echo "missing role-provisioning authority diagnostic" >&2
  exit 1
}

public_connect="$(
  ROBIN_DATABASE_URL="$database_url" ruby scripts/psql-with-url.rb -Atqc \
    "SELECT has_database_privilege('public', current_database(), 'CONNECT')"
)"
[[ "$public_connect" == "f" ]] || {
  echo "database still permits unreviewed public connections" >&2
  exit 1
}
public_temporary="$(
  ROBIN_DATABASE_URL="$database_url" ruby scripts/psql-with-url.rb -Atqc \
    "SELECT has_database_privilege('public', current_database(), 'TEMPORARY')"
)"
[[ "$public_temporary" == "f" ]] || {
  echo "database still permits unreviewed temporary schemas" >&2
  exit 1
}

readonly_mode="$(
  ROBIN_DATABASE_URL="$readonly_url" ruby scripts/psql-with-url.rb -Atqc 'SHOW transaction_read_only'
)"
[[ "$readonly_mode" == "on" ]] || {
  echo "read-only role does not default to read-only transactions" >&2
  exit 1
}

ROBIN_DATABASE_URL="$live_control_url" ruby scripts/psql-with-url.rb --set ON_ERROR_STOP=1 \
  --command "UPDATE live_scheduler_work SET value = 'completed' WHERE id = 1" >/dev/null
ROBIN_DATABASE_URL="$provisioner_url" ruby scripts/psql-with-url.rb --set ON_ERROR_STOP=1 \
  --command "UPDATE robinhood_execution_bindings SET value = 'ready' WHERE id = 1" >/dev/null
ROBIN_DATABASE_URL="$signer_url" ruby scripts/psql-with-url.rb --set ON_ERROR_STOP=1 \
  --command "UPDATE robinhood_signer_transactions SET value = 'submitted' WHERE id = 1" >/dev/null

rejects() {
  local url="$1"
  local sql="$2"
  local message="$3"
  if ROBIN_DATABASE_URL="$url" ruby scripts/psql-with-url.rb \
    --set ON_ERROR_STOP=1 --command "$sql" >/dev/null 2>&1; then
    echo "$message" >&2
    exit 1
  fi
}

rejects "$readonly_url" "INSERT INTO users VALUES (1)" "read-only role acquired write authority"
rejects "$readonly_url" "CREATE TABLE forbidden_table (id bigint)" "read-only role acquired DDL authority"
rejects "$readonly_url" "CREATE TEMP TABLE forbidden_temp (id bigint)" "read-only role acquired temporary schema authority"
rejects "$readonly_url" "CREATE ROLE forbidden_role" "read-only role acquired role authority"
rejects "$readonly_url" "SELECT forbidden_function()" "read-only role acquired function authority"
rejects "$live_control_url" \
  "UPDATE execution_control SET value = 'active' WHERE id = 1" \
  "live-control role acquired control-plane write authority"
rejects "$live_control_url" \
  "UPDATE robinhood_signer_transactions SET value = 'forbidden' WHERE id = 1" \
  "live-control role acquired signer journal authority"
rejects "$provisioner_url" \
  "UPDATE robinhood_signer_transactions SET value = 'forbidden' WHERE id = 1" \
  "custody provisioner acquired signer journal authority"
rejects "$signer_url" \
  "UPDATE robinhood_execution_bindings SET value = 'forbidden' WHERE id = 1" \
  "custody signer acquired provisioner authority"

echo "database role separation: ok"
