# Venue: Lighter public feed

The runtime consumes Lighter's read-only public WebSocket feed for perp market data. The connector
(`runtime/src/lighter.rs`) carries no authentication, signing, order submission, or write path; it
reads market metadata over REST and market data over the public stream, and hands each frame to the
capture layer verbatim.

## Endpoints

- WebSocket: `wss://mainnet.zklighter.elliot.ai/stream` (supplied by config; `?readonly=true` is
  available for restricted regions).
- REST metadata: `GET {api}/api/v1/orderBookDetails`.

## Subscriptions

Per traded market the connector subscribes to `order_book`, `ticker`, `trade`, and `market_stats`,
plus one shared `height` channel. A single connection may hold at most 100 subscriptions, so the
connector enforces `4 * market_count + 1 <= 100` before connecting and refuses to start otherwise
(a configuration fault, not something a reconnect can fix).

## Frames

All handled frames are decoded through typed structures. Required fields are non-optional, so a
missing one is rejected rather than silently defaulted; omitempty fields (for example a trade's
`taker_fee`) are optional; prices and sizes are kept as strings to avoid lossy float conversion;
unknown fields are ignored so additive protocol changes do not break decoding.

| Frame | `type` | Notes |
| --- | --- | --- |
| Order book | `update/order_book` | First frame is a full snapshot; later frames are deltas. |
| Ticker (BBO) | `update/ticker` | `s`/`a`/`b` symbol, ask, bid. |
| Trade | `update/trade` | `trades` and `liquidation_trades` arrays. |
| Market stats | `update/market_stats` | Funding, open interest, mark/index, best bid/ask. |
| Height | `update/height` | Chain block height. |
| Acknowledgement | `subscribed/*` | Must carry a non-empty `channel`. |

### Timestamps

Order-book and ticker frames carry a top-level millisecond `timestamp` and a microsecond
`last_updated_at`; the inner `order_book` object carries its own microsecond `last_updated_at`.
These are decoded into distinct fields and never conflated.

### Order-book reconstruction and continuity

The order book is reconstructed from its snapshot and each subsequent delta. A delta's `begin_nonce`
must equal the book's current `nonce`; a mismatch is a gap. On a gap the local book is invalidated
and a typed continuity error is returned, which drops the connection so the next attempt starts from
a fresh snapshot. The `offset` field is not used for continuity: it increases but is documented as
not guaranteed continuous across servers.

### Keepalive and reconnect

The server closes idle connections after two minutes. The connector sends a WebSocket ping every 60
seconds. Transient disconnects, gaps, and failed acknowledgements reconnect with capped exponential
backoff and equal jitter (500 ms base, 30 s ceiling).

## Documented schema ambiguities

- **Snapshot vs delta share one type.** Both the initial snapshot and subsequent deltas use
  `update/order_book`; there is no discriminator field. The connector treats the first frame per
  channel as the snapshot and every later frame as a delta.
- **Precision, minimum size, margin fractions, and fees are not in the feed.** They do not appear in
  any WebSocket frame. They are read from the REST `orderBookDetails` metadata (parsed strictly,
  without lossy defaults, for the markets actually traded) and, for fills, from trade objects.
- **No documented error frame.** The reference does not define an error frame shape, so the
  connector does not model one; unrecognized frame types are ignored rather than treated as errors.
- **Acknowledgement shape varies by channel.** The reference shows `subscribed/*` acknowledgements
  for some channels but describes the order-book channel's first message as the snapshot itself. The
  connector validates any `subscribed/*` frame it receives (requiring a non-empty `channel`) and
  otherwise relies on the snapshot as the effective acknowledgement.
