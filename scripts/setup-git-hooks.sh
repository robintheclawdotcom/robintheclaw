#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

git config --local user.name robintheclaw
git config --local user.email robintheclaw@users.noreply.github.com
git config --local core.hooksPath .githooks

chmod +x .githooks/pre-commit .githooks/pre-push \
  scripts/check-git-identity.sh scripts/check-no-leaks.sh scripts/setup-git-hooks.sh

echo "Git identity and hooks configured."
