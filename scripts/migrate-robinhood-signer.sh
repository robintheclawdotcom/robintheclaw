#!/usr/bin/env bash
set -euo pipefail

migration="signer/robinhood/migrations/0001_journal.sql"
if [[ ! -f "$migration" ]]; then
  echo "missing Robinhood signer migration: $migration" >&2
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

if [[ "${1:-}" == "--check" ]]; then
  sha256_file "$migration" >/dev/null
  echo "Robinhood signer migration manifest is valid"
  exit 0
fi

database_url="${1:-}"
if [[ -z "$database_url" ]]; then
  echo "Robinhood signer database URL is required" >&2
  exit 1
fi

digest="$(sha256_file "$migration")"
absolute_path="$(pwd)/$migration"
wrapper="$(mktemp)"
trap 'rm -f "$wrapper"' EXIT

psql "$database_url" --set ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE IF NOT EXISTS robin_signer_schema_migrations (
    migration TEXT PRIMARY KEY,
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
SQL

{
  printf '\\set ON_ERROR_STOP on\n'
  printf 'BEGIN;\n'
  printf "SELECT pg_advisory_xact_lock(hashtextextended('robin-robinhood-signer-schema', 0));\n"
  printf "SELECT EXISTS (SELECT 1 FROM robin_signer_schema_migrations WHERE migration = '%s') AS applied \\\\gset\n" "$migration"
  printf '\\if :applied\n'
  printf "SELECT sha256 = '%s' AS checksum_valid FROM robin_signer_schema_migrations WHERE migration = '%s' \\\\gset\n" "$digest" "$migration"
  printf '\\if :checksum_valid\n'
  printf '\\else\n'
  printf '\\echo Robinhood signer migration checksum mismatch\n'
  printf '\\quit 1\n'
  printf '\\endif\n'
  printf '\\else\n'
  printf '\\ir %s\n' "$absolute_path"
  printf "INSERT INTO robin_signer_schema_migrations (migration, sha256) VALUES ('%s', '%s');\n" "$migration" "$digest"
  printf '\\endif\n'
  printf 'COMMIT;\n'
} > "$wrapper"

psql "$database_url" --file "$wrapper"
