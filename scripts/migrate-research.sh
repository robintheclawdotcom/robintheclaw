#!/usr/bin/env bash
set -euo pipefail

migrations=(
  runtime/migrations/0001_capture.sql
  runtime/migrations/0002_durable_archive.sql
  runtime/migrations/0003_paper_agent.sql
  runtime/migrations/0004_agent_fanout.sql
  runtime/migrations/0005_secure_archive_partition_function.sql
)

if [[ "${1:-}" == "--check" ]]; then
  expected="$(printf '%s\n' "${migrations[@]}" | LC_ALL=C sort)"
  actual="$(find runtime/migrations -maxdepth 1 -type f -name '*.sql' | LC_ALL=C sort)"
  [[ "$expected" == "$actual" ]] || {
    echo "research migration manifest does not match runtime/migrations" >&2
    diff -u <(printf '%s\n' "$expected") <(printf '%s\n' "$actual") || true
    exit 1
  }
  echo "research migration manifest is valid"
  exit 0
fi

bash scripts/run-ordered-migrations.sh \
  "${1:-}" robin_research_schema_migrations robin-research-schema "${migrations[@]}"
