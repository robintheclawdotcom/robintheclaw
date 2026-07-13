#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

manifests=()
while IFS= read -r manifest; do
  manifests+=("$manifest")
done < <(find . -mindepth 2 -maxdepth 2 -name Cargo.toml -not -path './contracts/*' -print | sort)
if [ "${#manifests[@]}" -eq 0 ]; then
  echo "no Rust crates found" >&2
  exit 1
fi

for manifest in "${manifests[@]}"; do
  echo "checking ${manifest#./}"
  cargo fmt --manifest-path "$manifest" --check
  cargo clippy --manifest-path "$manifest" --all-targets --locked -- -D warnings
  cargo test --manifest-path "$manifest" --all-targets --locked
done
