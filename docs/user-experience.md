# User experience and account model

## Product promise

Robin gives a user one place to open a strategy account, bring capital together from multiple
wallets, activate a personal vault, follow real performance, and remain in control. The default
path starts with email or passkey and requires no extension, seed phrase, RPC configuration, CLI,
environment variables, manual network switch, or gas balance.

The first release targets Robinhood Chain testnet. It exposes the real personal vault, test asset
balance, mandate, opportunity feed, and account activity. Position and P&L surfaces remain empty
until venue execution produces real data.

## Routes

| Route | Experience |
| --- | --- |
| `/` | Public product narrative and `Open app` action |
| `/docs` | Architecture, research, contracts, operations, and developer documentation |
| `/app` | Total value, available balance, deployed capital, P&L, strategy, opportunities, positions, and recent activity |
| `/app/onboarding` | Resumable account, recovery, wallet, and one-click vault setup |
| `/app/strategy` | Mandate state, vault details, funding, withdrawal, and positions |
| `/app/activity` | Cursor-paginated onchain and account history |
| `/app/wallets` | Linked wallets, funding source, sync, conflict recovery, and unlinking |
| `/app/settings` | Recovery, display currency, notifications, and session control |

## Identity and wallet ownership

The Privy DID is the durable account key in the application database. Sign-in supports email,
passkey, Google, Apple, and EVM wallet. Privy creates an embedded EVM signer for every user. Its
address is used as the Alchemy EIP-7702 smart-account address and permanently owns that factory
version's personal vault.

MetaMask, Phantom, Robinhood Wallet, and other detected EVM wallets can be progressively linked.
They appear as portfolio and funding sources. The active funding preference changes which
connected wallet signs an approval-and-deposit batch; it never changes the vault owner. The API
refreshes wallet ownership directly from Privy and rejects an address already attached to another
Robin user. Recovery sends the user back to the existing account instead of merging identities.

Before first funding, the account must have a verified email or passkey. Embedded wallets cannot
be unlinked from the application. External wallets can be unlinked; if the active funding wallet
is removed, the preference returns to the embedded wallet.

## Onboarding state machine

```text
account -> recovery -> vault prepared -> wallet confirmation
   -> included -> receipt verified -> dashboard
                    |
                    +-> server confirmation delayed -> resume from saved call ID
```

1. Privy restores or creates the user and embedded signer.
2. `POST /api/v1/me/wallets/sync` resolves verified wallets server-side, persists the stable smart
   account, and reports recovery state.
3. `POST /api/v1/vaults/prepare` checks recovery, duplicate database state, `vaultOf(owner)`, faucet
   claim state, and `predictVault(owner)`.
4. The response returns an ordered call batch: faucet claim when needed, token approval, factory
   creation, and deposit. Sponsorship credentials remain on the server.
5. The Alchemy smart-wallet client submits the calls through the authenticated same-origin wallet
   proxy on Robinhood Chain testnet and waits for inclusion.
6. The browser saves the call ID before server confirmation. `POST /api/v1/vaults/confirm` resolves
   the transaction receipt, verifies the exact factory event, owner, asset, version, and deployed
   vault relationships, then persists the result idempotently.
7. Reloading or returning after an interruption retries confirmation with the saved call ID. It
   never prepares a second vault when one already exists onchain.

The interface distinguishes account expiry, missing recovery, wallet conflicts, a disconnected
funding signer, a pending operation, failed inclusion, and an included operation awaiting server
confirmation. Each state offers a direct recovery action.

## Dashboard data contract

All dashboard values come from the authenticated API. The response includes `environment` and
`asOf`, integer-string token amounts, decimals, and symbols. The API reads smart-account and vault
balances from provider RPC, reads `halted` and `remaining` from the personal guard, joins the
current basis research feed, and returns persisted activity.

`pnl` is null until real positions exist. Position arrays remain empty before venue execution.
The interface renders that absence directly and never substitutes fixtures, estimated returns, or
synthetic balances.

## Capital and strategy controls

- `Add funds` uses the selected connected wallet to sign a sponsored approval-and-deposit batch.
- `Withdraw` sends the owner-only vault call from the embedded smart account back to that account.
- `Start strategy` and `Pause strategy` send `MandateGuard.setHalted` from the smart-account owner.
- Sponsorship policy restricts targets to the configured faucet, asset, factory, vaults, and guards,
  and restricts selectors to the product actions.
- Every included vault, guard, and anchor event is indexed into the user's activity stream.

## Application services

The Next.js application uses Privy, Alchemy Wallet APIs, viem, and TanStack Query. It creates an
HTTP-only same-origin session cookie and forwards authenticated requests through `/api/app/*` to
the private Rust service. It forwards Wallet API methods through `/api/wallet`, where the session,
chain, account, call batch, targets, and selectors are checked before sponsorship is added. The
Rust API validates every Privy JWT, resolves linked accounts from Privy, and stores product state
in the dedicated `robin-app` PostgreSQL database.

The API service does not reuse the research collector database and does not accept a client wallet
list as proof. It has no user private key, embedded-wallet export, or owner signing route.

## Accessibility and responsive behavior

The application supplies a skip link, visible keyboard focus, semantic headings and tables,
explicit form labels, status text in addition to color, reduced-motion handling, and mobile
navigation. Content reflows into a single column at narrow widths and remains usable at 320px.
Wallet and transaction identifiers are shortened visually while full values remain available from
the API or explorer link.

## Product instrumentation

Production instrumentation should measure sign-in completion, recovery completion, wallet-link
success, onboarding completion time, call inclusion, confirmation delay, API latency, dashboard
load, and action success. Event payloads may include internal user IDs, route names, durations,
status codes, chain ID, and action type. They must not include JWTs, cookies, signatures, email
addresses, full Privy responses, or provider credentials.
