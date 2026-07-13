# Venue: Lighter public feed

The runtime consumes Lighter's read-only public WebSocket feed for perp market data. The connector
(`runtime/src/lighter.rs`) carries no authentication, signing, order submission, or write path; it
reads market metadata over REST and market data over the public stream, and hands each frame to the
capture layer verbatim.

## Endpoints

- WebSocket: `wss://mainnet.zklighter.elliot.ai/stream` (supplied by config; `?readonly=true` is
  available for restricted regions).
- REST metadata: `GET {api}/api/v1/orderBookDetails`.
- Protocol reference: [WebSocket API](https://apidocs.lighter.xyz/docs/websocket-reference).
- Connection limits: [rate limits](https://apidocs.lighter.xyz/docs/rate-limits).

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
| Acknowledgement | `subscribed/*` | Must match a requested channel and its channel type. |
| Server error | `error`, `error/*`, `*/error` | Captured as source health, then forces reconnect. |

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

Prices and sizes remain decimal strings. The connector validates their exact syntax without binary
floating-point conversion. A zero size removes a level; negative, exponent, non-finite, empty, or
otherwise malformed values invalidate the book and force a fresh snapshot.

### Keepalive and reconnect

The server closes idle connections after two minutes. The connector sends a WebSocket ping every 60
seconds. Malformed required fields, unexpected channels, duplicate acknowledgements, server errors,
disconnects, and continuity gaps reconnect with capped exponential backoff and equal jitter (500 ms
base, 30 s ceiling).

Every text frame receives a connection-scoped session ID and monotonically increasing local event
ID. Venue nonces, heights, and timestamps remain independent source sequences. Acknowledgements,
unknown additive frame types, and server errors are retained as source-health events so protocol
changes are visible in the archive.

## Documented schema ambiguities

- **Snapshot vs delta share one type.** Both the initial snapshot and subsequent deltas use
  `update/order_book`; there is no discriminator field. The connector treats the first frame per
  channel as the snapshot and every later frame as a delta.
- **Precision, minimum size, margin fractions, and fees are not in the feed.** They do not appear in
  any WebSocket frame. They are read from the REST `orderBookDetails` metadata (parsed strictly,
  without lossy defaults, for the markets actually traded) and, for fills, from trade objects.
- **No documented error-frame schema.** The reference does not define a stable error payload. The
  connector accepts the conservative `error`, `error/*`, and `*/error` envelope family, retains the
  raw frame, and reconnects. The optional code remains untyped JSON to avoid a lossy assumption.
- **Acknowledgement shape varies by channel.** The reference shows `subscribed/*` acknowledgements
  for some channels but describes the order-book channel's first message as the snapshot itself. The
  connector validates any acknowledgement against the requested channel and treats the first valid
  data frame as implicit activation when no acknowledgement is sent.
