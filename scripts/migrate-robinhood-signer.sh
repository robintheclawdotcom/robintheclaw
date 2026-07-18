#!/usr/bin/env bash
set -euo pipefail

migrations=(signer/robinhood/migrations/0001_journal.sql)

if [[ "${1:-}" == "--check" ]]; then
  expected="$(printf '%s\n' "${migrations[@]}" | LC_ALL=C sort)"
  actual="$(find signer/robinhood/migrations -maxdepth 1 -type f -name '*.sql' | LC_ALL=C sort)"
  [[ "$expected" == "$actual" ]] || {
    echo "Robinhood signer migration manifest does not match migration directory" >&2
    diff -u <(printf '%s\n' "$expected") <(printf '%s\n' "$actual") || true
    exit 1
  }
  echo "Robinhood signer migration manifest is valid"
  exit 0
fi

bash scripts/run-ordered-migrations.sh \
  "${1:-}" robin_signer_schema_migrations robin-robinhood-signer-schema "${migrations[@]}"
