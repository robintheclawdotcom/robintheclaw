# Application testnet

Robin's no-code application contracts are deployed on Robinhood Chain testnet, chain ID 46630.
The deployment gives each Privy-backed strategy account a deterministic personal vault, its own
mandate guard and attestation anchor, plus a fixed test-asset claim for one-operation onboarding.

## Deployed contracts

| Contract | Address |
| --- | --- |
| TestUSDG | `0x0fD95bD301b65f4F6Cc3354315793194941EeF77` |
| TestAssetFaucet | `0xc1609C2B1C1F0bb518Cb2a8B6e21116008351191` |
| PersonalStrategyVaultFactory v1 | `0x98afbbe4a9a0149d34A4A9dEeD8D3767D65d150E` |

The factory uses default agent `0xB614B34D6102F60c402e2AeCca36c338975f9280`, a 1,000 tUSDG
rolling cap, and a 24-hour window. The faucet provides one 1,000 tUSDG claim per strategy account.

The authoritative machine-readable record is
[`deployments/ux-testnet.json`](../deployments/ux-testnet.json). Runtime bytecode and factory/faucet
wiring were read back through the provider RPC after deployment. The factory deployment is
[available in the explorer](https://explorer.testnet.chain.robinhood.com/tx/0x6b01b81e956d194db99535d07ec213debf3f86ecd450beac52c86a5e9b16d03a).

## Application path

```text
Privy signer -> EIP-7702 strategy account -> PersonalStrategyVaultFactory
                                                |
                                                +-> personal vault
                                                    +-> MandateGuard
                                                    +-> AttestationAnchor
```

The private application API uses these addresses to predict vaults, build onboarding batches,
verify factory receipts, read balances and mandate state, and restore interrupted onboarding.

The application service layer is live on paid Render resources: the public Next.js service talks
to a private Rust API backed by the dedicated `robin-app` PostgreSQL database. Robinhood Chain
reads use Alchemy PAYG with bounded 10,000-block activity indexing. The final activation step for
one-click onboarding is attaching and enabling the restricted Alchemy sponsorship policy.
