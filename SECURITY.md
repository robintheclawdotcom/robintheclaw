# Security Policy

Robin the Claw includes smart contracts that custody funds and off-chain code that can move value.
Please treat security reports accordingly.

## Reporting a vulnerability

Do not open a public issue for a security vulnerability.

Report privately through either:

- GitHub Security Advisories: use the "Report a vulnerability" button under the Security tab of
  this repository, or
- Email: hello@robintheclaw.com

Include a description, the affected component and version, and a reproduction if you have one. We
aim to acknowledge within 72 hours and will keep you updated as we investigate.

## Scope

In scope:

- `contracts/` (`RwaStrategyVault`, `MandateRiskManagerV1`, `UniswapV4SpotAdapter`,
  `SequencerGate`, governance/deployment scripts, personal testnet vaults, and anchors)
- `verifier/` (record hashing, Merkle construction, onchain verification)
- key handling and any code path that signs or submits transactions

Out of scope:

- vendored dependencies under `contracts/lib/` (report upstream)
- issues that require a privileged operator key already being compromised
- the third-party venues the agent trades on (Uniswap, Lighter) and Robinhood Chain itself

## Disclosure

We follow coordinated disclosure. We will agree on a disclosure timeline with the reporter and
credit reporters who wish to be named once a fix is released.
