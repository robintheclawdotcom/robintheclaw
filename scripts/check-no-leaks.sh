#!/usr/bin/env bash
# Fails if tracked files contain absolute home paths, private-key material, or any pattern in the
# IDENTITY_DENYLIST env (newline-separated regexes, supplied at runtime so the denylist itself
# never lives in the tree). Runs in CI and as a pre-push check.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

fail=0
EXCLUDES=(':!contracts/lib/**' ':!**/*.lock' ':!**/package-lock.json')

scan() {
  local label="$1" pattern="$2" hits
  hits=$(git grep -nIE "$pattern" -- . "${EXCLUDES[@]}" 2>/dev/null || true)
  if [ -n "$hits" ]; then
    echo "LEAK (${label}):"
    echo "$hits"
    fail=1
  fi
}

scan "absolute home path" '/(Users|home)/[A-Za-z0-9._-]+/'
scan "private key block" 'BEGIN [A-Z ]*PRIVATE KEY'
scan "hardcoded hex secret" '(secret|priv|private)[_-]?key["'"'"' ]*[:=][ "'"'"']*0x[0-9a-fA-F]{64}'

if [ -n "${IDENTITY_DENYLIST:-}" ]; then
  while IFS= read -r pat; do
    [ -n "$pat" ] && scan "denylist" "$pat"
  done <<<"$IDENTITY_DENYLIST"
fi

if [ "$fail" -eq 0 ]; then
  echo "leak scan: clean"
else
  echo "leak scan: FAILED"
  exit 1
fi
