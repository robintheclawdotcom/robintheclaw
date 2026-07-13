# Developer documentation

Robin the Claw is an autonomous delta-neutral trading stack for tokenized markets on Robinhood
Chain. The repository brings together market intelligence, deterministic trade planning,
onchain execution, continuous event capture, research models, and a no-code strategy application.

## Documents

- [Architecture](architecture.md): the market intelligence, planning, execution, and record stack.
- [User experience](user-experience.md): sign-in, onboarding, dashboard, multi-wallet funding, and recovery.
- [Developer guide](developer-guide.md): local setup, validation commands, and configuration.
- [Operations](operations.md): roles, release procedure, and operational response.
- [Security model](security-model.md): assets, trust boundaries, and controls.
- [Testnet foundation](testnet-proof.md): the deployed onchain contract and record pipeline.
- [Venue integration](venue-gates.md): the path from market data to live venue support.
- [Research runtime](research-runtime.md): high-frequency capture, raw evidence, and shadow execution.
- [Edge research methodology](research-methodology.md): model hierarchy, RWA inputs, and implementation roadmap.
- [Mainnet readiness audit](production-audit-mainnet-readiness.md): the execution-readiness roadmap.

## Current foundation

1. The product layer connects Privy identity, Alchemy smart accounts, personal vaults, and a real account dashboard.
2. Market intelligence, continuous event capture, and trade planning run as independent components.
3. The model roadmap covers cointegration, Ornstein-Uhlenbeck spreads, Kalman hedge ratios,
   hidden-Markov regimes, and portfolio covariance.
4. Venue adapters and the complete position lifecycle are the next execution milestone.
5. Records are available as a supporting tool for research and operations.
6. Secrets belong in ignored local files or managed secret storage.
