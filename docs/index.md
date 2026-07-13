# Developer documentation

Robin the Claw is building a delta-neutral trading stack for tokenized markets on Robinhood
Chain. The repository brings together market intelligence, deterministic trade planning,
on-chain execution foundations, high-frequency research, and record integrity tooling.

## Documents

- [Architecture](architecture.md): the market intelligence, planning, execution, and record stack.
- [Developer guide](developer-guide.md): local setup, validation commands, and configuration.
- [Operations](operations.md): roles, release procedure, and operational response.
- [Security model](security-model.md): assets, trust boundaries, and controls.
- [Testnet foundation](testnet-proof.md): the deployed on-chain contract and record pipeline.
- [Venue integration](venue-gates.md): the path from market data to live venue support.
- [Research runtime](research-runtime.md): high-frequency capture, raw evidence, and shadow execution.
- [Edge research methodology](research-methodology.md): model hierarchy, RWA inputs, and implementation roadmap.
- [Mainnet readiness audit](production-audit-mainnet-readiness.md): the execution-readiness roadmap.

## Current foundation

1. The testnet stack connects custody, strategy roles, and on-chain records.
2. Market intelligence, high-frequency research, and trade planning run as independent components.
3. Venue adapters and the complete order lifecycle are the next execution milestone.
4. Records are available as a supporting tool for research and operations.
5. Secrets belong in ignored local files or managed secret storage.
