#!/usr/bin/env bash
# Verifies the public repository carries the expected governance and policy files.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

req=(
  LICENSE
  README.md
  SECURITY.md
  CONTRIBUTING.md
  CODE_OF_CONDUCT.md
  GOVERNANCE.md
  MAINTAINERS.md
  SUPPORT.md
  RELEASING.md
  AGENTS.md
  .githooks/pre-commit
  .githooks/pre-push
  scripts/check-git-identity.sh
  scripts/check-no-leaks.sh
  scripts/setup-git-hooks.sh
  runtime/Cargo.toml
  runtime/migrations/0001_capture.sql
  runtime/src/bin/collector.rs
  .github/CODEOWNERS
  .github/PULL_REQUEST_TEMPLATE.md
  .github/dependabot.yml
  .github/workflows/ci.yml
  .github/workflows/identity-firewall.yml
)

miss=0
for f in "${req[@]}"; do
  if [ ! -f "$f" ]; then
    echo "missing: $f"
    miss=1
  fi
done

if [ "$miss" -eq 0 ]; then
  echo "repo standards: ok"
else
  echo "repo standards: FAILED"
  exit 1
fi
