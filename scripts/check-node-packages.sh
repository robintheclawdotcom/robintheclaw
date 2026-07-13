#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

manifests=()
while IFS= read -r manifest; do
  manifests+=("$manifest")
done < <(find . -mindepth 2 -maxdepth 2 -name package.json -print | sort)
if [ "${#manifests[@]}" -eq 0 ]; then
  echo "no Node packages found" >&2
  exit 1
fi

for manifest in "${manifests[@]}"; do
  directory="$(dirname "$manifest")"
  echo "checking ${directory#./}"
  npm --prefix "$directory" ci
  if node -e 'const p=require(process.argv[1]); process.exit(p.scripts?.[process.argv[2]] ? 0 : 1)' "$manifest" test; then
    npm --prefix "$directory" test
  fi
  if node -e 'const p=require(process.argv[1]); process.exit(p.scripts?.[process.argv[2]] ? 0 : 1)' "$manifest" build; then
    npm --prefix "$directory" run build
  fi
done
