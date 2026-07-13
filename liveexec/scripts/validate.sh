#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

cd "$root/liveexec"
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go test -race ./...

cd "$root"
cargo run --quiet --manifest-path execution/Cargo.toml --bin validate-intent -- liveexec/testdata/pair-intent-v2.json
