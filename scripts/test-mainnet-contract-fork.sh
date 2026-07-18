#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
: "${RH_MAINNET_RPC:?set RH_MAINNET_RPC to a Robinhood Chain mainnet endpoint}"

(cd contracts && forge test --match-contract RwaUserMainnetForkTest -vv)
