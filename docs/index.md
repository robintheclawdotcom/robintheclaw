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
- [Mainnet contract deployment](mainnet-deployment.md): live typed contracts, governance, verification, and staged activation.
- [Testnet foundation](testnet-proof.md): the deployed onchain contract and record pipeline.
- [Application testnet](ux-testnet.md): the deployed personal-vault factory, faucet, and no-code onboarding path.
- [Venue integration](venue-gates.md): the path from market data to live venue support.
- [Research runtime](research-runtime.md): high-frequency capture, raw evidence, and shadow execution.
- [Mainnet paper trading](paper-trading-operations.md): production workers, strategy evidence, launch verification, and recovery.
- [Edge research methodology](research-methodology.md): model hierarchy, RWA inputs, and implementation roadmap.
- [Mainnet readiness audit](production-audit-mainnet-readiness.md): the execution-readiness roadmap.

## Current foundation

1. The deployed product layer connects Privy identity, Alchemy smart accounts, personal vaults,
   and a real account dashboard.
2. Market intelligence, continuous event capture, and trade planning run as independent components.
3. The model roadmap covers cointegration, Ornstein-Uhlenbeck spreads, Kalman hedge ratios,
   hidden-Markov regimes, and portfolio covariance.
4. The typed production vault, risk manager, spot adapter, Safe, and timelock are live and
   source-verified on Robinhood Chain mainnet.
5. The production deployment remains halted and unfunded while market configuration, execution
   authority, and operational evidence progress through staged activation.
6. Records are available as a supporting tool for research and operations.
7. Secrets belong in ignored local files or managed secret storage.

The public web service, private Rust API, dedicated product database, PAYG chain access, and
personal-vault contracts are live for production testnet work. The separate typed mainnet contract
layer is live at the addresses in [`deployments/mainnet.json`](../deployments/mainnet.json).
Onboarding uses account-funded ETH by default. Attaching a restricted Alchemy policy switches the
same validated call path to sponsored gas without changing the signer or vault owner.
