#!/usr/bin/env bash
set -euo pipefail

database_url="${1:-}"
ledger="${2:-}"
lock_name="${3:-}"
shift 3 || true
migrations=("$@")

if [[ -z "$database_url" ]]; then
  echo "database owner URL is required" >&2
  exit 1
fi
if [[ ! "$ledger" =~ ^robin_[a-z0-9_]+_schema_migrations$ ]]; then
  echo "migration ledger name is invalid" >&2
  exit 1
fi
if [[ ! "$lock_name" =~ ^robin-[a-z0-9-]+-schema$ ]]; then
  echo "migration lock name is invalid" >&2
  exit 1
fi
if (( ${#migrations[@]} == 0 )); then
  echo "at least one migration is required" >&2
  exit 1
fi

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

for migration in "${migrations[@]}"; do
  if [[ ! -f "$migration" ]]; then
    echo "missing migration: $migration" >&2
    exit 1
  fi
  sha256_file "$migration" >/dev/null
done

ROBIN_DATABASE_URL="$database_url" ruby scripts/psql-with-url.rb \
  --set ON_ERROR_STOP=1 --set ledger="$ledger" <<'SQL'
SELECT format(
    'CREATE TABLE IF NOT EXISTS %I (
        migration TEXT PRIMARY KEY,
        sha256 TEXT NOT NULL CHECK (sha256 ~ ''^[0-9a-f]{64}$''),
        applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
    )',
    :'ledger'
)
\gexec
SQL

for migration in "${migrations[@]}"; do
  digest="$(sha256_file "$migration")"
  absolute_path="$(cd "$(dirname "$migration")" && pwd)/$(basename "$migration")"
  wrapper="$(mktemp)"
  trap 'rm -f "$wrapper"' EXIT
  {
    printf '\\set ON_ERROR_STOP on\n'
    printf '\\set ledger %s\n' "$ledger"
    printf 'BEGIN;\n'
    printf "SELECT pg_advisory_xact_lock(hashtextextended('%s', 0));\n" "$lock_name"
    printf "SELECT EXISTS (SELECT 1 FROM :ledger WHERE migration = '%s') AS applied \\\\gset\n" "$migration"
    printf '\\if :applied\n'
    printf "SELECT sha256 = '%s' AS checksum_valid FROM :ledger WHERE migration = '%s' \\\\gset\n" "$digest" "$migration"
    printf '\\if :checksum_valid\n'
    printf '\\else\n'
    printf '%s\n' "DO \$\$ BEGIN RAISE EXCEPTION 'migration checksum mismatch: $migration'; END \$\$;"
    printf '\\endif\n'
    printf '\\else\n'
    printf '\\ir %s\n' "$absolute_path"
    printf "INSERT INTO :ledger (migration, sha256) VALUES ('%s', '%s');\n" "$migration" "$digest"
    printf '\\endif\n'
    printf 'COMMIT;\n'
  } > "$wrapper"
  ROBIN_DATABASE_URL="$database_url" ruby scripts/psql-with-url.rb --file "$wrapper"
  rm -f "$wrapper"
  trap - EXIT
done
