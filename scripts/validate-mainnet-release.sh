#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

node config/validate-live-policy.mjs
node --test ops/mainnet-live/validate.test.mjs
node --test ops/mainnet-live/promotion-ledger/ledger.test.mjs
node ops/mainnet-live/validate.mjs
ruby scripts/validate-blueprint.rb
cargo run --quiet --locked --manifest-path research/Cargo.toml \
  --bin strategy-manifest-gate -- config/strategies/basis-aapl-v1.manifest.json
bash liveexec/scripts/validate.sh
