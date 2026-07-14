# Sequencer publisher

Each instance independently verifies Robinhood Chain health and reports it to the
2-of-3 `QuorumSequencerFeed`. A report is healthy only when:

- the source and transaction RPCs both identify chain `4663`;
- latest and finalized source blocks are fresh and within the configured lag;
- observed block numbers and hashes are monotonic; and
- the transaction RPC returns the same hash for the source's finalized block.

The source and transaction endpoints must use different providers. The three
Render workers must also use three independent source providers and dedicated,
funded publisher keys. Publisher keys have no vault, transfer, or withdrawal
role.

Before every send, the worker verifies the feed runtime code, publisher binding,
quorum constants, calldata, signer, chain ID, gas, fee, and maximum transaction
cost. PostgreSQL stores the signed transaction before broadcast, holds a
per-publisher advisory lock, and replays only the exact journaled transaction
after an ambiguous send. Unexpected nonce advancement permanently quarantines
that publisher until an operator reconciles the journal and chain history.

`/healthz` and `/metrics` are available on `SEQUENCER_LISTEN_ADDRESS` inside the
worker network.
