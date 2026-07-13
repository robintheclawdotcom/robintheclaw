#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

node config/validate-live-policy.mjs
cargo run --quiet --locked --manifest-path research/Cargo.toml \
  --bin strategy-manifest-gate -- config/strategies/basis-aapl-v1.manifest.json
