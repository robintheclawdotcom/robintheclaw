# Security model

## Assets to protect

- Custodied USDG balances in `RwaStrategyVault` and testnet balances in personal vaults.
- Safe, timelock, guardian, agent, and personal-vault owner authority.
- Venue credentials and any collateral held outside the vault.
- Integrity of the disclosed trade-log record.
- Availability of the owner halt action and public verification path.
- Integrity and confidentiality of raw market evidence, research datasets, and shadow records.
- Privy sessions, linked-wallet ownership, personal smart-account identity, and application data.

## Trust boundaries

Market data, RPC responses, exchange APIs, local JSONL observations, and published records are
untrusted inputs. The engine is deterministic but does not establish an economic edge on its own.
The vault enforces call boundaries, while the runtime and validation gates determine whether a
candidate is eligible for further review. The verifier confirms a commitment, not the truthfulness
of unobserved market data.

## Controls in place

- Source-verified typed mainnet contract graph deployed halted and unfunded with a zero agent.
- Canonical 2-of-3 Safe treasury, 48-hour timelock config authority, and restrict-only guardian.
- No agent-selected target, selector, route, recipient, arbitrary calldata, or declared notional.
- Intent replay protection and binding to asset, side, amounts, deadline, config version, multiplier,
  and minimum oracle round.
- Fail-closed sequencer, oracle freshness, oracle pause, corporate-action, slippage, inventory,
  turnover, fresh gross-exposure, market-count, and operating-mode checks.
- Canonical Universal Router and Permit2 runtime code-hash pinning.
- Exact temporary approvals, mandatory allowance cleanup, measured vault deltas, and zero adapter retention.
- Safe-only funding and terminal recovery; Safe-only immediate agent revocation.
- One-time vault anchor configuration and strict append-only anchor sequence.
- Canonical integer-scaled records with finite/range validation before hashing.
- Testnet proof vault with no execution target.
- Ignored secret paths and a repository leak scan before release.
- Private worker-only R2 credentials and a no-public-IP research database.
- Runtime contains no signer, wallet material, or venue write client.
- ES256 validation on every authenticated API request and an HTTP-only same-origin session cookie.
- Server-side Alchemy credential injection with an authenticated method allowlist and call-plan checks.
- Same-origin enforcement, bounded request bodies, per-session throttling, CSP, HSTS, and structured error events.
- Privy server-side wallet resolution, cross-user address uniqueness, and no automatic account merge.
- Stable smart-account vault ownership independent of the active funding wallet.
- Idempotent receipt verification against the exact factory event and deployed contract state.
- Optional sponsorship limited by managed target, selector, and per-account policy.
- Dedicated application database with user-scoped reads and writes.

## Known limitations

The production vault does not implement public deposits, shares, pooled NAV, a perp adapter, or
off-chain fill reconciliation. USDG treasury funding is Safe-controlled, and the deployed spot
adapter has no configured market. Perpetual execution, margin management, paired-leg repair, and
continuous reconciliation remain in the private execution layer.

The Safe owner set is still bootstrap custody and must move to device-separated operational owners
before capital activation. The one-time sequencer gate remains unbound until a reviewed official
source exists. USDG is treated as one settlement dollar by the contract; depeg handling is an
operational halt condition.

The anchor cannot force record publication. It can prove a published batch was committed, and it
can make an absence of a matching publication visible to observers.

## Required controls for capital activation

1. Verified venue contract/API specification and authenticated testnet order lifecycle.
2. Simulate-before-send, explicit slippage, partial-fill hedge/unwind, and reconciliation logic.
3. Perp collateral, leverage, liquidation, funding, and margin-health controls.
4. Sequencer/oracle staleness policy and a venue-mark fallback with bounded exposure.
5. Independent smart-contract audit and an operational key-management review.
6. Human per-trade approval for the first capped mainnet experiments.

The typed mainnet boundary already enforces the onchain portion of this list. The remaining controls
govern its staged transition from a halted deployment to a separately approved canary.

The generic `MandateGuard`/personal-vault call path is testnet-only and is not a production venue
adapter. Mainnet custody uses only `RwaStrategyVault`, `MandateRiskManagerV1`,
`UniswapV4SpotAdapter`, and the associated Safe/timelock governance boundary.
