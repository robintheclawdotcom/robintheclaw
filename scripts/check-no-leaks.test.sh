#!/usr/bin/env bash
set -euo pipefail

root=$(git rev-parse --show-toplevel)
scanner="$root/scripts/check-no-leaks.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

git -C "$tmp" init -q
mkdir -p "$tmp/bin" "$tmp/home"
printf '%s\n' '#!/usr/bin/env bash' 'printf "%s\n" "${FAKE_IDENTITY:-}"' >"$tmp/bin/id"
chmod +x "$tmp/bin/id"

scan() {
  (
    cd "$tmp"
    env \
      FAKE_IDENTITY="${1:-}" \
      GITHUB_ACTIONS=true \
      HOME="$tmp/home" \
      IDENTITY_DENYLIST="${2:-}" \
      PATH="$tmp/bin:$PATH" \
      bash "$scanner"
  )
}

printf '%s\n' "strategy runner" >"$tmp/fixture.txt"
git -C "$tmp" add fixture.txt
scan runner >/dev/null
scan "" >/dev/null

printf '%s\n' "private-account" >"$tmp/fixture.txt"
if scan private-account >"$tmp/output" 2>&1; then
  echo "non-empty runtime identity was not detected" >&2
  exit 1
fi
grep -F "LEAK (local account name)" "$tmp/output" >/dev/null

printf '%s\n' "strategy runner" >"$tmp/fixture.txt"
if scan runner 'strategy[[:space:]]+runner' >"$tmp/output" 2>&1; then
  echo "explicit denylist was not enforced" >&2
  exit 1
fi
grep -F "LEAK (denylist)" "$tmp/output" >/dev/null

echo "identity leak scan tests: ok"
