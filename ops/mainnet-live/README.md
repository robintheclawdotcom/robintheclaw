# Mainnet live-execution observability

This directory contains the active-canary v1 observability contract:

- `metrics/contract.v1.json` defines required metric ownership, labels, units, and semantics;
- `prometheus/rules.v1.yaml` defines stage-reset-aware alerts;
- `grafana/` contains a file-provisioned dashboard and Prometheus data source;
- `validate.mjs` and `validate.test.mjs` reject missing coverage, unsafe alert direction changes,
  incomplete stage-reset labels, and malformed dashboard artifacts.

Run the focused checks with:

```sh
node --test ops/mainnet-live/validate.test.mjs
node ops/mainnet-live/validate.mjs
```

Set `PROMETHEUS_URL` only in the Grafana deployment environment. These files do not provision a
Prometheus server, a paging receiver, an incident controller, or any metric producer.
