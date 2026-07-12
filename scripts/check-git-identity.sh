#!/usr/bin/env bash
# Enforces the repository's public Git identity without exposing local identity values.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

expected_name="robintheclaw"
safe_email='^[a-z0-9._+-]+@(users\.noreply\.github\.com|robintheclaw\.com)$'

fail=0

check_identity() {
  local name="$1" email="$2" ref="$3"
  if [ "$name" != "$expected_name" ]; then
    echo "invalid Git author name in $ref"
    fail=1
  fi
  if [[ ! "$email" =~ $safe_email ]]; then
    echo "invalid Git author email in $ref"
    fail=1
  fi
}

case "${1:-}" in
  --current)
    check_identity "$(git config --get user.name || true)" "$(git config --get user.email || true)" "local config"
    ;;
  --range)
    range="${2:?missing revision range}"
    while IFS=$'\t' read -r sha name email; do
      check_identity "$name" "$email" "$sha"
    done < <(git log --format='%H%x09%an%x09%ae' "$range")
    ;;
  *)
    echo "usage: $0 --current | --range <revision-range>" >&2
    exit 2
    ;;
esac

if [ "$fail" -ne 0 ]; then
  exit 1
fi

echo "Git identity: clean"
