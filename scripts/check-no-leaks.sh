#!/usr/bin/env bash
# Fails if repository content contains private material, local identifiers, or patterns supplied
# through IDENTITY_DENYLIST. Runtime-derived values keep local identity data out of the tree.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

fail=0
EXCLUDES=(':!contracts/lib/**' ':!**/*.lock' ':!**/package-lock.json')
mode="worktree"
revision=""

case "${1:-}" in
  "") ;;
  --cached) mode="cached" ;;
  --ref)
    mode="ref"
    revision="${2:?missing revision}"
    ;;
  *)
    echo "usage: $0 [--cached | --ref <revision>]" >&2
    exit 2
    ;;
esac

search() {
  local options=("$@")
  if [ "$mode" = "cached" ]; then
    git grep --cached "${options[@]}" -- . "${EXCLUDES[@]}" 2>/dev/null || true
  elif [ "$mode" = "ref" ]; then
    git grep "${options[@]}" "$revision" -- . "${EXCLUDES[@]}" 2>/dev/null || true
  else
    git grep "${options[@]}" -- . "${EXCLUDES[@]}" 2>/dev/null || true
  fi
}

scan_regex() {
  local label="$1" pattern="$2" hits
  hits=$(search -lIE "$pattern")
  if [ -n "$hits" ]; then
    echo "LEAK (${label}) in:"
    echo "$hits"
    fail=1
  fi
}

scan_literal() {
  local label="$1" value="$2" hits
  [ -n "$value" ] || return 0
  hits=$(search -lIF "$value")
  if [ -n "$hits" ]; then
    echo "LEAK (${label}) in:"
    echo "$hits"
    fail=1
  fi
}

scan_regex "absolute home path" '/(Users|home)/[A-Za-z0-9._-]+/'
scan_regex "private key block" 'BEGIN [A-Z ]*PRIVATE KEY'
scan_regex "hardcoded hex secret" '(secret|priv|private)[_-]?key["'"'"' ]*[:=][ "'"'"']*0x[0-9a-fA-F]{64}'

scan_literal "local home directory" "${HOME:-}"
scan_literal "local account name" "$(id -un)"
scan_literal "global Git name" "$(git config --global --get user.name || true)"
scan_literal "global Git email" "$(git config --global --get user.email || true)"

if [ -n "${IDENTITY_DENYLIST:-}" ]; then
  while IFS= read -r pat; do
    [ -n "$pat" ] && scan_regex "denylist" "$pat"
  done <<<"$IDENTITY_DENYLIST"
fi

if [ "$fail" -eq 0 ]; then
  echo "identity leak scan: clean"
else
  echo "identity leak scan: FAILED"
  exit 1
fi
