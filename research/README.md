# Research artifacts and promotion gates

This crate defines immutable dataset and model records, embargoed walk-forward folds, regime
vetoes, and the evidence required to promote a strategy. It does not train a model or place an
order.

Promotion is fail-closed. An incomplete audit, legal review, restore drill, evidence window, or
statistical result keeps the strategy out of canary eligibility.

```bash
cargo test --manifest-path research/Cargo.toml
```
