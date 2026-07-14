#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

node config/validate-live-policy.mjs
node --test scripts/validate-engineering-canary.test.mjs
node scripts/validate-engineering-canary.mjs
node --test ops/mainnet-live/validate.test.mjs
node --test ops/mainnet-live/promotion-ledger/ledger.test.mjs
node ops/mainnet-live/validate.mjs
ruby scripts/validate-blueprint.rb
bash scripts/migrate-execution.sh --check
bash scripts/migrate-robinhood-signer.sh --check
cargo run --quiet --locked --manifest-path research/Cargo.toml \
  --bin strategy-manifest-gate -- config/strategies/basis-aapl-v1.manifest.json
cargo run --quiet --locked --manifest-path research/Cargo.toml \
  --bin promotion-gate -- config/engineering-canary-evidence.json
bash liveexec/scripts/validate.sh
(cd publisher && go test ./...)
(cd provisioner/lighter && go test ./...)
(cd provisioner/robinhood && go test ./...)
(cd signer/lighter && go test ./...)
(cd signer/robinhood && go test ./...)
(cd runtime/exit-quote-publisher && go test ./...)
(cd runtime/live-evaluation && go test ./...)
(cd runtime/live-scheduler && go test ./...)
(cd runtime/sequencer-publisher && go test ./...)
(cd ops/mainnet-live/restrictctl && go test ./...)
