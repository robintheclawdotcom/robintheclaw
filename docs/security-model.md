# Security model

## Assets to protect

- Custodied USDG balances in isolated `RwaUserStrategyVaultV1` instances and the historical operator vault.
- User-owner, registry, guardian, agent, Safe, and timelock authority.
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

- Non-upgradeable per-user mainnet graphs with immutable owner and treasury bindings.
- `MainnetExecutionRegistry` bindings for approved factories, execution accounts, graphs, and agents.
- One non-exportable KMS execution key and one user-owned Lighter subaccount per execution account.
- Historical source-verified singleton graph retained only for the operator canary.
- Canonical 2-of-3 Safe treasury, 48-hour timelock config authority, and restrict-only guardian.
- No agent-selected target, selector, route, recipient, arbitrary calldata, or declared notional.
- Intent replay protection and binding to asset, side, amounts, deadline, config version, multiplier,
  and minimum oracle round.
- Fail-closed sequencer, oracle freshness, oracle pause, corporate-action, slippage, inventory,
  turnover, fresh gross-exposure, market-count, and operating-mode checks.
- Canonical Universal Router and Permit2 runtime code-hash pinning.
- Exact temporary approvals, mandatory allowance cleanup, measured vault deltas, and zero adapter retention.
- Owner-only withdrawal and terminal recovery; owner-only immediate agent revocation.
- One-time vault anchor configuration and strict append-only anchor sequence.
- Canonical integer-scaled records with finite/range validation before hashing.
- Historical proof vault with no execution target.
- Ignored secret paths and a repository leak scan before release.
- Private worker-only R2 credentials and a no-public-IP research database.
- Runtime contains no signer, wallet material, or venue write client.
- ES256 validation on every authenticated API request and an HTTP-only same-origin session cookie.
- Server-side Alchemy credential injection with an authenticated method allowlist and call-plan checks.
- Same-origin enforcement, bounded request bodies, per-session throttling, CSP, HSTS, and structured error events.
- Privy server-side wallet resolution, cross-user address uniqueness, and no automatic account merge.
- Stable smart-account vault ownership independent of the active funding wallet.
- Idempotent receipt verification against the exact factory event and deployed contract state.
- Optional sponsorship limited by managed target, selector, and per-account policy; owner-paid gas is the default fallback.
- Dedicated application database with user-scoped reads and writes.

## Known limitations

The live strategy is intentionally fixed to long AAPL Stock Token and short the matching Lighter
perpetual. It does not accept user-selected markets, leverage, routes, calldata, thresholds, or
strategy code. The vault is not a pooled fund and does not issue shares. Perpetual execution,
margin management, paired-leg repair, and continuous reconciliation run in the private execution
layer rather than inside the EVM vault.

The historical singleton remains halted and unfunded until used for an operator canary; it is not a
customer custody path. USDG is treated as one settlement dollar by the contract, so depeg evidence
restricts admission operationally. Oracle or sequencer quorum failure also fails closed.

The anchor cannot force record publication. It can prove a published batch was committed, and it
can make an absence of a matching publication visible to observers.

## Live admission controls

1. Verified account, factory, vault, signer, Lighter subaccount, route, and code-hash bindings.
2. Simulate-before-send, explicit slippage, partial-fill repair, bounded unwind, and reconciliation.
3. Current collateral, maintenance margin, liquidation distance, funding, and position evidence.
4. Fresh executable quotes plus fail-closed oracle and sequencer quorum.
5. Aligned account-scoped EVM and Lighter nonces with no unknown orders, positions, or sends.
6. No open internal critical or high contract, execution, signer, key, or recovery finding.
7. `ACTIVE` global, strategy, and account controls, with reduce-only recovery always available.

User launch is the explicit instruction to start the fixed strategy; per-trade approval is not
required.

The generic `MandateGuard`/personal-vault call path remains a historical proof path and is not a
production venue adapter. Customer mainnet custody uses only graphs created by the approved
`RwaUserVaultFactoryV1` release and bound through `MainnetExecutionRegistry`.
