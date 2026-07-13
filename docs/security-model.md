# Security model

## Assets to protect

- Custodied ERC-20 balances in `StrategyVault`.
- Owner and agent signing authority.
- Venue credentials and any collateral held outside the vault.
- Integrity of the disclosed trade-log record.
- Availability of the owner halt action and public verification path.
- Integrity and confidentiality of raw market evidence, research datasets, and shadow records.

## Trust boundaries

Market data, RPC responses, exchange APIs, local JSONL observations, and published records are
untrusted inputs. The engine is deterministic but does not establish an economic edge on its own.
The vault enforces call boundaries, while the runtime and validation gates determine whether a
candidate is eligible for further review. The verifier confirms a commitment, not the truthfulness
of unobserved market data.

## Controls in place

- Explicit production asset/router inputs; no silent testnet defaults.
- Separate owner, agent, and funder addresses.
- Target and selector allowlist, bytecode check, rolling notional cap, and halt flag.
- One-time vault anchor configuration and strict append-only anchor sequence.
- Canonical integer-scaled records with finite/range validation before hashing.
- Testnet proof vault with no execution target.
- Ignored secret paths and a repository leak scan before release.
- Private worker-only R2 credentials and a no-public-IP research database.
- Runtime contains no signer, wallet material, or venue write client.

## Known limitations

The current vault does not implement public deposits, shares, NAV, allowances for a router, fill
reconciliation, margin management, or a perp adapter. It is therefore not a complete live-trading
custody system. A malicious or compromised owner can change the mandate; this is a deliberate
human-control boundary, not a decentralized governance model.

The anchor cannot force record publication. It can prove a published batch was committed, and it
can make an absence of a matching publication visible to observers.

## Required controls before live trading

1. Verified venue contract/API specification and authenticated testnet order lifecycle.
2. Simulate-before-send, explicit slippage, partial-fill hedge/unwind, and reconciliation logic.
3. Perp collateral, leverage, liquidation, funding, and margin-health controls.
4. Sequencer/oracle staleness policy and a venue-mark fallback with bounded exposure.
5. Independent smart-contract audit and an operational key-management review.
6. Human per-trade approval for the first capped mainnet experiments.

Until all six exist, the correct production mode is no execution.

The generic vault call path is not a production venue adapter. Before any live deployment it must
be replaced with typed, venue-specific intent enforcement that binds asset, route, recipient,
maximum input, minimum output, deadline, and slippage to each call.
