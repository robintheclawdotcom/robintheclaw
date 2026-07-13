# Developer documentation

Robin the Claw is a testnet-first, delta-neutral RWA basis research system. It is not a public
vault, investment product, or live trading service. The repository deliberately separates
measurement, deterministic decisioning, custody boundaries, and public verification.

## Documents

- [Architecture](architecture.md): component boundaries, data flow, and invariants.
- [Developer guide](developer-guide.md): local setup, validation commands, and configuration.
- [Operations](operations.md): role separation, release procedure, and incident actions.
- [Security model](security-model.md): assets, trust boundaries, threats, and controls.
- [Testnet proof](testnet-proof.md): the deployed no-execution proof path and independent check.
- [Venue gates](venue-gates.md): what must be verified before any order path exists.
- [Research runtime](research-runtime.md): private high-frequency capture, raw evidence, and shadow execution.

## Non-negotiable operating rules

1. No mainnet deployment or order path is enabled from this repository today.
2. Testnet uses a clearly named `tUSDG` fixture, never an assumed USDG address.
3. The deployed proof vault has no allowlisted venue. It can anchor a synthetic record but cannot
   execute a trade.
4. A verifier result proves that disclosed records match a commitment. It does not prove a return,
   fill quality, or strategy edge.
5. Secrets belong in ignored local files or managed secret storage. They are never committed.
6. The research runtime has no signing capability and no configuration switch for live trading.
