# Account state publisher

This service reconstructs account-scoped Lighter and Robinhood Chain state and publishes short-lived, HMAC-authenticated snapshots to the execution coordinator and product readiness API. It starts disabled. It never sends orders, transfers, or withdrawals.

The Lighter adapter is pinned to `https://mainnet.zklighter.elliot.ai` and uses only the documented account, active-order, trade, and next-nonce REST endpoints. Its credential is a read-only token stored in a mode-0600 file. Lighter documents that read-only tokens can access authenticated data without transaction-signing authority, that API-key nonces are account-specific, and that REST rate limits require backoff: [API keys](https://apidocs.lighter.xyz/docs/api-keys), [rate limits](https://apidocs.lighter.xyz/docs/rate-limits), [WebSocket reference](https://apidocs.lighter.xyz/docs/websocket-reference).

The Robinhood adapter requires two different RPC provider hostnames. At the same finalized block it compares chain ID, sync status, safe/finalized heads, canonical registry/vault bindings, vault code hash, owner and execution gas, USDG balance, risk state, and every receipt in the account-bound journal. A difference, reorg, missing receipt, failed receipt, or rate limit fails closed. Robinhood documents chain ID 4663, standard EVM JSON-RPC, ETH gas, the production RPC choices, and canonical token addresses: [connecting](https://docs.robinhood.com/chain/connecting/), [token contracts](https://docs.robinhood.com/chain/contracts/).

## Activation boundary

`policy_active` is deliberately published as `false`. The state publishers cannot authorize capital. An operator-controlled, signed activation authority still has to prove the audit, legal and venue, oracle, route, key, reconciliation, alerting, exit, and observation-period gates before replacing that evidence.

The official Lighter account WebSocket channels do not document one contiguous sequence shared by orders, positions, collateral, and trades. `StreamTracker` therefore fails closed on session or sequence gaps and requires a complete REST reconstruction before health can recover; the current daemon performs the authoritative REST reconstruction every cycle. A reviewed adapter for a documented cross-channel sequence is still required before relying on WebSocket deltas to reduce polling load for larger cohorts.

## Configuration

Set `ACCOUNT_PUBLISHER_ENABLED=true` and point `ACCOUNT_PUBLISHER_CONFIG_FILE` at a non-secret JSON file. HMAC keys, Lighter read-only tokens, receipt journals, and signer-owned expected-nonce files are referenced by path and must be mode 0600. The coordinator and application HMAC keys must be different. `ACCOUNT_PUBLISHER_POLL_MILLISECONDS` may be 4000–4500; the default is 4500, below Lighter's documented standard-account REST limit for one authenticated account.

Each account binding contains:

- an opaque execution account ID and, for user accounts, the same UUID in `readinessExecutionAccountId`;
- the exact Lighter account, API-key, and AAPL market indexes;
- the Lighter read-only token path and signer-owned next-nonce path;
- the canonical registry, factory, vault, risk, adapter, owner, and signer addresses;
- the expected vault code hash and minimum balances;
- a mode-0600 receipt journal path whose JSON is `{"vault":"0x...","hashes":["0x..."]}`.

The receipt journal is re-read on every cycle. A vault mismatch, duplicate hash, or malformed hash blocks publication. The signer-owned nonce file must be replaced atomically after nonce reservation; a missing or mismatched value makes `nonce_aligned` false.

The pre-existing `singleton-mainnet-canary` coordinator account is the only binding allowed to omit `readinessExecutionAccountId`; it has no product-owned agent record. All user accounts must publish to both destinations under the same UUID.

```bash
go test ./...
go vet ./...
docker build -t robin-account-publisher .
```
