# Mainnet live internal P0/P1 audit

Date: 2026-07-14

## Scope

This pass covered the per-user mainnet vault graph, registry and timelock activation, owner withdrawal and terminal recovery, AAPL reference quorum, fixed swap route, token approvals, reentrancy, deterministic deployment, executable quote authority, and AAPL relay.

The review found no unresolved code-fixable P0. Four P1 issues were fixed and regression-tested. This result covers repository behavior only; it does not prove that a deployed graph, configured external dependency, funded signer, or venue account behaves correctly on mainnet.

## Fixed P1 findings

### Chainlink round IDs were truncated

The quote authority decoded `roundId` and `answeredInRound` as `uint64`, but Chainlink returns `uint80` and uses the high bits for proxy phases. A valid phase-aware round could be truncated and compared incorrectly.

Fixed in:

- `liveexec/quoteauthority/rpc.go`
- `liveexec/quoteauthority/live_adapter.go`
- `liveexec/quoteauthority/live_adapter_test.go`

Round identifiers are now unsigned arbitrary-precision integers, compared without truncation, and preserved in the signed quote evidence.

### Quote freshness could be laundered

The dual-RPC adapter stamped a finalized block observation with local request time. Requoting could therefore make an old finalized block appear fresh.

Fixed in:

- `liveexec/quoteauthority/rpc.go`
- `liveexec/quoteauthority/live_adapter_test.go`

The observation time now comes from the agreed block timestamp and is preserved across requotes.

### Partial exits violated the one-episode turnover policy

The vault permitted repeated buys and partial sells. That allowed an episode to consume turnover in shapes the policy did not intend and could strand a small position after the daily cap was reached.

Fixed in:

- `contracts/src/RwaUserStrategyVaultV1.sol`
- `contracts/test/RwaUserVaultFactoryV1.t.sol`

An entry now requires zero tracked inventory. An exit must sell the full tracked inventory. The regression covers duplicate entry, partial exit rejection, complete exit, and a subsequent episode.

### Timelock activation did not validate authority topology

The release script validated delay and code hash but did not prove that the expected safe held proposer, canceller, and executor authority, or that open execution was disabled.

Fixed in:

- `contracts/script/DeployRwaUserMainnetV1.s.sol`
- `contracts/test/DeployRwaUserMainnetV1.t.sol`

Activation now requires the configured governance safe to hold proposer, canceller, and executor roles; rejects zero-address open roles; requires self-admin on the timelock; and rejects governance-safe admin authority.

## Verified properties

- Per-user CREATE2 salts include owner and policy identity; the registry accepts only the canonical graph emitted by an approved factory.
- Owner, agent, guardian, relayer, and governance roles are distinct. The agent can trade through the fixed adapter but cannot withdraw.
- Global registry restrictions are checked before local risk authorization. Reactivating the global registry does not lift a locally restricted vault.
- Normal owner withdrawal requires a halted, flat, agent-revoked graph. Terminal recovery is owner-only, irreversible, and cannot restart execution.
- The route pins token order, fee `10000`, tick spacing `200`, zero hooks, pool ID, router, Permit2, and runtime code hashes.
- Vault and adapter use measured balance deltas, exact approvals, and cleanup to zero. Router failure reverts approval changes. External entrypoints are either owner-only, vault-only, or reentrancy-guarded.
- The reference feed requires exact agreement from two of three fresh publishers. Missing quorum, disagreement, replay, same-round mutation, or stale data fails closed.
- Quote evidence binds execution account, evaluation, strategy manifest, route, oracle policy, risk policy, Lighter market, Robinhood graph, and observation age.
- AAPL relay source reads require two agreeing finalized Arbitrum RPC responses, a pinned source address and runtime code hash, expected decimals and description, monotonic rounds, bounded fees, journaled nonce ownership, and a target-contract identity check.

## Residual P1 risks

These are not removed by enabling the repository configuration:

1. Automated full exit depends on fresh AAPL quorum, a healthy sequencer feed, and usable router liquidity. During a prolonged dependency failure the owner can terminally recover raw AAPL, but the vault cannot guarantee an atomic conversion to USDG.
2. Runtime code-hash pinning does not pin an implementation behind an external proxy. USDG proxy upgrades and Chainlink proxy aggregator changes can preserve the proxy runtime hash. The relay also checks Chainlink description, decimals, and round behavior, while vault transfers use measured deltas, but those checks do not eliminate issuer or proxy-governance risk.
3. OpenZeppelin `AccessControl` is not role-enumerable in this deployment interface. The script proves required and forbidden known memberships, but cannot prove that no undisclosed address has a timelock role. Activation must compare deployment events and the transaction receipt against the intended role set.
4. Three relay regions and separate RPC hostnames do not prove organizational independence. Publisher keys, RPC providers, and operators must be independently controlled in the deployed configuration.
5. The exact `AAPL_SOURCE_FEED_CODE_HASH` is a required fail-closed deployment input. It was not retrieved in this sandbox and must not be guessed or replaced with a placeholder.
6. No funded mainnet transaction, real Lighter association, or complete live entry/exit/withdrawal was executed in this sandbox. A successful repository test run is not equivalent to a live round trip.

Operationally, the release deployment is reproducible through deterministic salts but is not a rerunnable no-op once those CREATE2 addresses exist. Per-user factory deployment is the idempotent path. Target-chain receipt acceptance also depends on the configured RPC's reorg behavior; a post-receipt reorg should surface as nonce or state divergence and fail closed, but still requires operator reconciliation.

## Verification

Run from the consolidated worktree:

```text
cd contracts && forge test --offline
```

Result: 110 passed, 0 failed. This included 256-run fuzz tests and 256-run, 128,000-call invariant campaigns.

```text
cd runtime/sequencer-publisher
GOCACHE=/private/tmp/robin-go-cache go test -race ./...
GOCACHE=/private/tmp/robin-go-cache go vet ./...
GOCACHE=/private/tmp/robin-go-cache go build ./cmd/aapl-relay
```

Result: passed.

```text
cd liveexec
GOCACHE=/private/tmp/robin-go-cache go test ./quoteauthority -run 'TestLiveAdapter|TestCanonicalPool|TestDecode' -count=1
GOCACHE=/private/tmp/robin-go-cache go vet ./quoteauthority
GOCACHE=/private/tmp/robin-go-cache go build ./cmd/quote-authority
```

Result: passed. The full package's listener-backed tests cannot bind a local socket in this sandbox; the no-listener unit and regression set above passed.

```text
ruby scripts/validate-blueprint.rb
git diff --check
```

Result: blueprint policy clean; no whitespace errors.
