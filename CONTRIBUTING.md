# Contributing

Thanks for your interest. This repository is the public, verifiable core of Robin the Claw: the
contracts, the signal measurement, and the tools anyone can use to recompute the agent's record.
Trading strategy internals are not part of this repository.

## Build and test

Contracts (Foundry):

```bash
cd contracts && forge test
```

Verifier (Node 20+):

```bash
cd verifier && npm install && npm test
```

Signal scanners read live venues and need network access; they are not part of CI:

```bash
cd signal && npm install && node src/discover.mjs && node src/spot.mjs
```

## Before you open a pull request

Run the local checks that CI enforces:

```bash
cd contracts && forge fmt --check && forge test
cd verifier && npm test
bash scripts/check-no-leaks.sh
bash scripts/check-repo-standards.sh
```

## Conventions

- Commits are lowercase, imperative, and describe the change ("add mandate window rollover"), not
  the process. No attribution trailers.
- Solidity follows `forge fmt`. Keep contracts small and favor explicit reverts with named errors.
- JavaScript is ES modules, no framework. Keep tools dependency-light.
- Comments explain why, not what. Do not restate the code.

## What belongs here

Contracts, the verifier, the SDK, the signal measurement, and their tests and docs. Anything that
reveals live trading parameters, entry thresholds, or position state does not belong in a public
repository and will not be merged.

## License

By contributing you agree that your contributions are licensed under Apache-2.0.
