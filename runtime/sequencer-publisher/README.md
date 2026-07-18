# Sequencer publisher

Each instance independently verifies Robinhood Chain health and reports it to the
2-of-3 `QuorumSequencerFeed`. A report is healthy only when:

- the source and transaction RPCs both identify chain `4663`;
- latest and finalized source blocks are fresh and within the configured lag;
- observed block numbers and hashes are monotonic; and
- the transaction RPC returns the same hash for the source's finalized block;
- both RPCs return the pinned USDG proxy and implementation identities; and
- both RPCs return the pinned AAPL proxy, beacon, and implementation identities.

The source and transaction endpoints must use different providers. The three
Render workers must also use three independent source providers and dedicated,
funded publisher keys. Publisher keys have no vault, transfer, or withdrawal
role.

Robinhood Chain's observed Nitro finality trails the L2 head by roughly 15
minutes and thousands of L2 blocks. The production bounds allow at most 30
minutes or 25,000 blocks while the onchain quorum still requires reports less
than 60 seconds old. Either bound being exceeded reports the chain unhealthy.

Dependency checks run at both the common finalized block and the latest source
height. Any proxy slot, beacon response, implementation bytecode, proxy
bytecode, or RPC-view mismatch makes the publisher report unhealthy. Because
the onchain feed requires two fresh reports out of three, an upstream token
implementation change fails closed without waiting for L1 finality or relying
on the unchanged proxy runtime hash.

Before every send, the worker verifies the feed runtime code, publisher binding,
quorum constants, calldata, signer, chain ID, gas, fee, and maximum transaction
cost. PostgreSQL stores the signed transaction before broadcast, holds a
per-publisher advisory lock, and replays only the exact journaled transaction
after an ambiguous send. Unexpected nonce advancement permanently quarantines
that publisher until an operator reconciles the journal and chain history.

`/healthz` and `/metrics` are available on `SEQUENCER_LISTEN_ADDRESS` inside the
worker network.
