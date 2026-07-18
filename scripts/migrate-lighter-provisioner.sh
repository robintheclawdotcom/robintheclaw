#!/usr/bin/env bash
set -euo pipefail

migrations=(
  provisioner/lighter/migrations/0001_credentials.sql
  provisioner/lighter/migrations/0002_reject_reserved_api_keys.sql
  provisioner/lighter/migrations/0003_signing_nonce_claims.sql
  provisioner/lighter/migrations/0004_terminal_revocation.sql
)

if [[ "${1:-}" == "--check" ]]; then
  expected="$(printf '%s\n' "${migrations[@]}" | LC_ALL=C sort)"
  actual="$(find provisioner/lighter/migrations -maxdepth 1 -type f -name '*.sql' | LC_ALL=C sort)"
  [[ "$expected" == "$actual" ]] || {
    echo "Lighter provisioner migration manifest does not match migration directory" >&2
    diff -u <(printf '%s\n' "$expected") <(printf '%s\n' "$actual") || true
    exit 1
  }
  echo "Lighter provisioner migration manifest is valid"
  exit 0
fi

bash scripts/run-ordered-migrations.sh \
  "${1:-}" robin_lighter_schema_migrations robin-lighter-schema "${migrations[@]}"
