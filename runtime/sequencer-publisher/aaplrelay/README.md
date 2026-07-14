# AAPL reference relay

Each instance reads the official Chainlink AAPL/USD proxy on Arbitrum One at a block finalized by two independent RPCs. The block hash, proxy runtime code, `AAPL / USD` description, 8-decimal format, and complete round response must agree exactly. The publisher preserves the source round, answer, `updatedAt`, and `answeredInRound`; it never replaces source time with relay time.

Three independently keyed instances report to `QuorumAaplReferenceFeed` on Robinhood Chain. The contract requires exact 2-of-3 consensus, expires relay reports after 60 seconds, and rejects Chainlink source data older than 25 hours. The 25-hour limit is the feed's 24-hour heartbeat plus a one-hour delivery margin. Executable quotes remain independently limited to five seconds and must agree with the relayed reference.

Every signed EIP-1559 transaction is journaled in Postgres before broadcast. A publisher takes a Postgres advisory lock for its identity and quarantines itself on source regression or mutation, raw transaction mismatch, signer mismatch, or unexplained nonce advancement.
