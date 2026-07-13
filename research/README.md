# Research artifacts and promotion gates

This crate defines immutable dataset and model records, embargoed walk-forward folds, regime
vetoes, and the evidence required to promote a strategy. It does not train a model or place an
order.

Promotion is fail-closed. An incomplete audit, legal review, restore drill, evidence window, or
statistical result keeps the strategy out of canary eligibility.

Live strategy manifests are equally strict. `basis-aapl-v1` pins the source configuration, route,
oracle policy, risk policy, code revision, AAPL-only direction, and the $25-per-leg/$50-gross
limits. A changed field requires a new checksum and a separately promoted strategy version.

```bash
cargo test --manifest-path research/Cargo.toml
cargo run --manifest-path research/Cargo.toml --bin strategy-manifest-gate -- manifest.json
```
