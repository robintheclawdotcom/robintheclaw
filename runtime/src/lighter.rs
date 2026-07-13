//! Read-only connector for the zkLighter public WebSocket feed.
//!
//! Hardened against documented production protocol behavior: every frame is deserialized through
//! typed structures, the order book is reconstructed from its snapshot and validated against each
//! delta's nonce chain, malformed required fields are rejected, the subscription budget is
//! enforced before connecting, and disconnects reconnect with capped exponential backoff and
//! jitter. It carries no authentication, signing, or write path.
//!
//! Protocol reference: zkLighter WebSocket reference (frames `update/order_book`, `update/ticker`,
//! `update/trade`, `update/market_stats`, `update/height`, `subscribed/*`). Per-market precision,
//! minimum size, margin fractions, and fees are not present in any WebSocket frame; they are read
//! from the REST `orderBookDetails` metadata and parsed by [`MarketMetadata`]. See
//! `docs/venue-lighter.md`.

use crate::{Finality, MarketEventKind, RawMarketEvent};
use anyhow::Context;
use futures_util::{SinkExt, StreamExt};
use serde::de::DeserializeOwned;
use serde::Deserialize;
use serde_json::json;
use std::collections::HashMap;
use std::fmt;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::time::{interval, sleep, timeout, MissedTickBehavior};
use tokio_tungstenite::{connect_async, tungstenite::Message};
use tracing::{info, warn};

const CONNECTOR_VERSION: &str = env!("CARGO_PKG_VERSION");

/// Channels subscribed per market: order book, ticker, trade, market stats.
pub const CHANNELS_PER_MARKET: usize = 4;
/// Server-side ceiling on concurrent subscriptions for a single connection.
pub const MAX_SUBSCRIPTIONS: usize = 100;

const KEEPALIVE: Duration = Duration::from_secs(60);
const CONNECT_TIMEOUT: Duration = Duration::from_secs(10);
const BASE_BACKOFF_MS: u64 = 500;
const MAX_BACKOFF_MS: u64 = 30_000;

/// Typed failure modes of the public feed. Variants for which [`LighterError::requires_reconnect`]
/// is true invalidate connection state (including any reconstructed book) and trigger a backoff.
#[derive(Debug)]
pub enum LighterError {
    /// A frame could not be decoded into its typed structure, or a required field was absent.
    Decode(String),
    /// An order-book delta did not chain onto the previous nonce.
    ContinuityGap {
        channel: String,
        expected: u64,
        received: u64,
    },
    /// A subscription acknowledgement was missing or malformed.
    Acknowledgement(String),
    /// The requested subscription count exceeds what one connection may hold.
    SubscriptionBudget { requested: usize, limit: usize },
}

impl LighterError {
    /// Whether encountering this error should drop the connection and reconnect. A continuity gap
    /// or a failed acknowledgement means local state can no longer be trusted; a budget violation
    /// is a configuration fault that reconnecting cannot fix.
    pub fn requires_reconnect(&self) -> bool {
        matches!(
            self,
            LighterError::ContinuityGap { .. } | LighterError::Acknowledgement(_)
        )
    }
}

impl fmt::Display for LighterError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            LighterError::Decode(detail) => write!(f, "malformed Lighter frame: {detail}"),
            LighterError::ContinuityGap {
                channel,
                expected,
                received,
            } => write!(
                f,
                "order book continuity gap on {channel}: expected begin_nonce {expected}, received {received}"
            ),
            LighterError::Acknowledgement(detail) => {
                write!(f, "invalid Lighter subscription acknowledgement: {detail}")
            }
            LighterError::SubscriptionBudget { requested, limit } => write!(
                f,
                "subscription budget exceeded: {requested} requested, {limit} allowed"
            ),
        }
    }
}

impl std::error::Error for LighterError {}

// ---------------------------------------------------------------------------
// Typed frames. Required fields are non-optional so a missing one is rejected rather than
// silently defaulted; optional/omitempty fields are `Option`. Prices and sizes stay as strings to
// avoid any lossy float conversion. Unknown fields are ignored so additive protocol changes do
// not break decoding.

#[derive(Debug, Deserialize)]
struct Envelope {
    #[serde(rename = "type")]
    frame_type: String,
}

#[derive(Debug, Clone, Deserialize)]
pub struct Level {
    pub price: String,
    pub size: String,
}

#[derive(Debug, Deserialize)]
pub struct OrderBookFrame {
    pub channel: String,
    /// Top-level microsecond publish time; distinct from the millisecond `timestamp`.
    #[serde(rename = "last_updated_at")]
    pub last_updated_us: u64,
    pub offset: u64,
    pub order_book: OrderBookData,
    /// Top-level millisecond timestamp.
    #[serde(rename = "timestamp")]
    pub timestamp_ms: i64,
}

#[derive(Debug, Deserialize)]
pub struct OrderBookData {
    pub code: i64,
    pub asks: Vec<Level>,
    pub bids: Vec<Level>,
    pub offset: u64,
    pub nonce: u64,
    /// Inner microsecond update time.
    #[serde(rename = "last_updated_at")]
    pub last_updated_us: u64,
    pub begin_nonce: u64,
}

#[derive(Debug, Deserialize)]
pub struct TickerFrame {
    pub channel: String,
    #[serde(rename = "last_updated_at")]
    pub last_updated_us: u64,
    pub nonce: u64,
    pub ticker: Ticker,
    #[serde(rename = "timestamp")]
    pub timestamp_ms: i64,
}

#[derive(Debug, Deserialize)]
pub struct Ticker {
    #[serde(rename = "s")]
    pub symbol: String,
    #[serde(rename = "a")]
    pub ask: Level,
    #[serde(rename = "b")]
    pub bid: Level,
    #[serde(rename = "last_updated_at")]
    pub last_updated_us: u64,
}

#[derive(Debug, Deserialize)]
pub struct TradeFrame {
    pub channel: String,
    pub nonce: u64,
    pub trades: Vec<Trade>,
    #[serde(default)]
    pub liquidation_trades: Vec<Trade>,
}

#[derive(Debug, Deserialize)]
pub struct Trade {
    pub market_id: u64,
    pub size: String,
    pub price: String,
    pub timestamp: i64,
    #[serde(default)]
    pub trade_id: Option<u64>,
    #[serde(rename = "type", default)]
    pub trade_type: Option<String>,
    #[serde(default)]
    pub usd_amount: Option<String>,
    #[serde(default)]
    pub is_maker_ask: Option<bool>,
    #[serde(default)]
    pub block_height: Option<i64>,
    /// Present only when non-zero (omitempty on the wire).
    #[serde(default)]
    pub taker_fee: Option<i64>,
    #[serde(default)]
    pub maker_fee: Option<i64>,
}

#[derive(Debug, Deserialize)]
pub struct MarketStatsFrame {
    pub channel: String,
    pub market_stats: MarketStats,
    #[serde(rename = "timestamp")]
    pub timestamp_ms: i64,
}

#[derive(Debug, Deserialize)]
pub struct MarketStats {
    pub symbol: String,
    pub market_id: u64,
    pub index_price: String,
    pub mark_price: String,
    pub best_bid_price: String,
    pub best_ask_price: String,
    pub open_interest: String,
    /// Estimate of the upcoming funding payment.
    pub current_funding_rate: String,
    /// Last settled funding payment, applied at `funding_timestamp`.
    pub funding_rate: String,
    pub funding_timestamp: i64,
    #[serde(default)]
    pub mid_price: Option<String>,
    #[serde(default)]
    pub open_interest_limit: Option<String>,
    #[serde(default)]
    pub funding_clamp_small: Option<String>,
    #[serde(default)]
    pub funding_clamp_big: Option<String>,
    #[serde(default)]
    pub last_trade_price: Option<String>,
    #[serde(default)]
    pub base_interest_rate: Option<String>,
}

#[derive(Debug, Deserialize)]
pub struct HeightFrame {
    pub channel: String,
    pub height: i64,
    #[serde(rename = "timestamp")]
    pub timestamp_ms: i64,
}

#[derive(Debug, Deserialize)]
pub struct AckFrame {
    pub channel: String,
}

/// A decoded public frame. `Other` carries the raw type string for frames the connector does not
/// act on (they are still captured verbatim upstream).
#[derive(Debug)]
pub enum Frame {
    OrderBook(Box<OrderBookFrame>),
    Ticker(Box<TickerFrame>),
    Trade(Box<TradeFrame>),
    MarketStats(Box<MarketStatsFrame>),
    Height(HeightFrame),
    Ack(AckFrame),
    Other(String),
}

fn decode<T: DeserializeOwned>(text: &str) -> Result<T, LighterError> {
    serde_json::from_str(text).map_err(|err| LighterError::Decode(err.to_string()))
}

/// Decode a public frame from its wire text, rejecting any frame whose required fields are absent
/// or mistyped.
pub fn parse_frame(text: &str) -> Result<Frame, LighterError> {
    let envelope: Envelope = decode(text)?;
    let frame = match envelope.frame_type.as_str() {
        "update/order_book" => Frame::OrderBook(Box::new(decode(text)?)),
        "update/ticker" => Frame::Ticker(Box::new(decode(text)?)),
        "update/trade" => Frame::Trade(Box::new(decode(text)?)),
        "update/market_stats" => Frame::MarketStats(Box::new(decode(text)?)),
        "update/height" => Frame::Height(decode(text)?),
        kind if kind.starts_with("subscribed/") => Frame::Ack(validate_ack(text)?),
        other => Frame::Other(other.to_string()),
    };
    Ok(frame)
}

/// Validate a subscription acknowledgement, rejecting a missing or empty channel.
pub fn validate_ack(text: &str) -> Result<AckFrame, LighterError> {
    let ack: AckFrame =
        serde_json::from_str(text).map_err(|err| LighterError::Acknowledgement(err.to_string()))?;
    if ack.channel.trim().is_empty() {
        return Err(LighterError::Acknowledgement("empty channel".to_string()));
    }
    Ok(ack)
}

/// Total subscriptions for `market_count` markets (`4 * market_count + 1`, the `+1` being the
/// shared height channel), rejecting anything a single connection cannot hold.
pub fn subscription_budget(market_count: usize) -> Result<usize, LighterError> {
    let total = CHANNELS_PER_MARKET
        .checked_mul(market_count)
        .and_then(|channels| channels.checked_add(1))
        .filter(|total| *total <= MAX_SUBSCRIPTIONS)
        .ok_or(LighterError::SubscriptionBudget {
            requested: CHANNELS_PER_MARKET
                .saturating_mul(market_count)
                .saturating_add(1),
            limit: MAX_SUBSCRIPTIONS,
        })?;
    Ok(total)
}

// ---------------------------------------------------------------------------
// Order book reconstruction. The first frame on a channel is the full snapshot; each later frame
// is a delta whose `begin_nonce` must equal the book's current `nonce`. A gap invalidates the
// book and demands a reconnect.

/// A reconstructed order book for one market. Levels are held by price string; a delta level whose
/// size is numerically zero removes the price.
#[derive(Debug, Default)]
pub struct OrderBook {
    nonce: Option<u64>,
    bids: HashMap<String, String>,
    asks: HashMap<String, String>,
}

impl OrderBook {
    pub fn is_initialized(&self) -> bool {
        self.nonce.is_some()
    }

    pub fn nonce(&self) -> Option<u64> {
        self.nonce
    }

    pub fn level_count(&self) -> usize {
        self.bids.len() + self.asks.len()
    }

    /// Apply a frame: the first is taken as a snapshot, later frames as deltas with a nonce-chain
    /// check. On a gap the book is left invalidated and a typed [`LighterError::ContinuityGap`] is
    /// returned.
    pub fn apply(&mut self, channel: &str, data: &OrderBookData) -> Result<(), LighterError> {
        match self.nonce {
            None => {
                self.bids.clear();
                self.asks.clear();
                write_levels(&mut self.bids, &data.bids);
                write_levels(&mut self.asks, &data.asks);
                self.nonce = Some(data.nonce);
                Ok(())
            }
            Some(current) => {
                if data.begin_nonce != current {
                    self.invalidate();
                    return Err(LighterError::ContinuityGap {
                        channel: channel.to_string(),
                        expected: current,
                        received: data.begin_nonce,
                    });
                }
                write_levels(&mut self.bids, &data.bids);
                write_levels(&mut self.asks, &data.asks);
                self.nonce = Some(data.nonce);
                Ok(())
            }
        }
    }

    fn invalidate(&mut self) {
        self.nonce = None;
        self.bids.clear();
        self.asks.clear();
    }
}

fn write_levels(book: &mut HashMap<String, String>, levels: &[Level]) {
    for level in levels {
        let removed = level.size.parse::<f64>().is_ok_and(|size| size <= 0.0);
        if removed {
            book.remove(&level.price);
        } else {
            book.insert(level.price.clone(), level.size.clone());
        }
    }
}

// ---------------------------------------------------------------------------
// Reconnect timing: equal-jitter capped exponential backoff. The ceiling is deterministic and the
// jitter is supplied explicitly so both are unit-testable.

fn backoff_ceiling_ms(attempt: u32) -> u64 {
    let factor = 1u64.checked_shl(attempt.min(20)).unwrap_or(u64::MAX);
    BASE_BACKOFF_MS.saturating_mul(factor).min(MAX_BACKOFF_MS)
}

fn reconnect_delay_ms(attempt: u32, jitter_source: u64) -> u64 {
    let ceiling = backoff_ceiling_ms(attempt);
    let half = ceiling / 2;
    half + jitter_source % (half + 1)
}

fn jitter_source() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|elapsed| elapsed.subsec_nanos() as u64)
        .unwrap_or(0)
}

// ---------------------------------------------------------------------------
// Market metadata (REST). Precision, minimum size, margin fractions, and fees are absent from the
// WebSocket feed and are read here without lossy defaults for the markets actually traded.

#[derive(Debug, Clone, Deserialize)]
pub struct MarketMetadata {
    pub market_id: u64,
    pub symbol: String,
    pub market_type: String,
    pub status: String,
    pub supported_price_decimals: u32,
    pub supported_size_decimals: u32,
    pub min_base_amount: String,
    pub min_quote_amount: String,
    pub taker_fee: String,
    pub maker_fee: String,
    pub liquidation_fee: String,
    pub default_initial_margin_fraction: i64,
    pub min_initial_margin_fraction: i64,
    pub maintenance_margin_fraction: i64,
    pub closeout_margin_fraction: i64,
}

impl MarketMetadata {
    pub fn is_active_perp(&self) -> bool {
        self.market_type == "perp" && self.status == "active"
    }
}

#[derive(Debug, Deserialize)]
struct MarketFilter {
    symbol: Option<String>,
    market_type: Option<String>,
    status: Option<String>,
}

#[derive(Debug, Deserialize)]
struct MarketMetadataResponse {
    order_book_details: Vec<serde_json::Value>,
}

/// Parse the `orderBookDetails` response, returning strict typed metadata for every active perp
/// market among `wanted`. A wanted market whose metadata is malformed is an error, not a default.
pub fn parse_market_metadata(
    body: &[u8],
    wanted: &std::collections::HashSet<&str>,
) -> anyhow::Result<Vec<MarketMetadata>> {
    let response: MarketMetadataResponse =
        serde_json::from_slice(body).context("decode Lighter market metadata")?;
    let mut markets = Vec::new();
    for entry in response.order_book_details {
        let filter: MarketFilter =
            serde_json::from_value(entry.clone()).context("read Lighter market descriptor")?;
        let is_wanted = filter.market_type.as_deref() == Some("perp")
            && filter.status.as_deref() == Some("active")
            && filter
                .symbol
                .as_deref()
                .is_some_and(|symbol| wanted.contains(symbol));
        if !is_wanted {
            continue;
        }
        let metadata: MarketMetadata = serde_json::from_value(entry).with_context(|| {
            format!(
                "parse Lighter metadata for {}",
                filter.symbol.as_deref().unwrap_or("<unknown>")
            )
        })?;
        markets.push(metadata);
    }
    if markets.is_empty() {
        anyhow::bail!("no configured symbols are active Lighter perp markets");
    }
    Ok(markets)
}

fn channel_market_id(channel: &str) -> Option<u64> {
    channel.rsplit(':').next()?.parse().ok()
}

// ---------------------------------------------------------------------------

pub struct LighterFeed {
    websocket_url: String,
    api_url: String,
    symbols: Vec<String>,
    client: reqwest::Client,
}

impl LighterFeed {
    pub fn new(websocket_url: String, api_url: String, symbols: Vec<String>) -> Self {
        Self {
            websocket_url,
            api_url,
            symbols,
            client: reqwest::Client::builder()
                .connect_timeout(Duration::from_secs(5))
                .timeout(Duration::from_secs(10))
                .build()
                .expect("build Lighter HTTP client"),
        }
    }

    async fn market_metadata(&self) -> anyhow::Result<Vec<MarketMetadata>> {
        let url = format!(
            "{}/api/v1/orderBookDetails",
            self.api_url.trim_end_matches('/')
        );
        let body = self
            .client
            .get(url)
            .send()
            .await
            .context("fetch Lighter market metadata")?
            .error_for_status()
            .context("Lighter market metadata response")?
            .bytes()
            .await
            .context("read Lighter market metadata")?;
        let wanted = self
            .symbols
            .iter()
            .map(String::as_str)
            .collect::<std::collections::HashSet<_>>();
        parse_market_metadata(&body, &wanted)
    }

    /// Run the feed until an unrecoverable error, reconnecting on any transient failure with
    /// capped exponential backoff and jitter. A reconnect starts from a fresh snapshot, so no
    /// stale book survives a gap.
    pub async fn run<F, Fut>(&self, mut handle: F) -> anyhow::Result<()>
    where
        F: FnMut(RawMarketEvent) -> Fut,
        Fut: std::future::Future<Output = anyhow::Result<()>>,
    {
        let mut attempt: u32 = 0;
        loop {
            match self.stream_once(&mut handle).await {
                Ok(()) => {
                    warn!("Lighter websocket closed");
                    attempt = 0;
                }
                Err(err) => {
                    if let Some(LighterError::SubscriptionBudget { .. }) =
                        err.downcast_ref::<LighterError>()
                    {
                        return Err(err);
                    }
                    attempt = attempt.saturating_add(1);
                    warn!(%err, attempt, "Lighter feed error; reconnecting");
                }
            }
            let delay = reconnect_delay_ms(attempt, jitter_source());
            sleep(Duration::from_millis(delay)).await;
        }
    }

    async fn stream_once<F, Fut>(&self, handle: &mut F) -> anyhow::Result<()>
    where
        F: FnMut(RawMarketEvent) -> Fut,
        Fut: std::future::Future<Output = anyhow::Result<()>>,
    {
        let markets = self.market_metadata().await?;
        subscription_budget(markets.len())?;
        let market_ids = markets
            .iter()
            .map(|market| (market.market_id, market.symbol.clone()))
            .collect::<HashMap<_, _>>();

        let (socket, _) = timeout(CONNECT_TIMEOUT, connect_async(&self.websocket_url))
            .await
            .context("connect Lighter public websocket timed out")?
            .context("connect Lighter public websocket")?;
        let (mut write, mut read) = socket.split();

        for market_id in market_ids.keys() {
            for channel in ["order_book", "ticker", "trade", "market_stats"] {
                write
                    .send(Message::Text(
                        json!({ "type": "subscribe", "channel": format!("{channel}/{market_id}") })
                            .to_string()
                            .into(),
                    ))
                    .await
                    .context("subscribe Lighter channel")?;
            }
        }
        write
            .send(Message::Text(
                json!({ "type": "subscribe", "channel": "height" })
                    .to_string()
                    .into(),
            ))
            .await
            .context("subscribe Lighter height")?;
        info!(markets = market_ids.len(), "Lighter public feed connected");

        let mut keepalive = interval(KEEPALIVE);
        keepalive.set_missed_tick_behavior(MissedTickBehavior::Skip);
        keepalive.tick().await;
        let mut books: HashMap<String, OrderBook> = HashMap::new();

        loop {
            let message = tokio::select! {
                _ = keepalive.tick() => {
                    write
                        .send(Message::Ping(Vec::new().into()))
                        .await
                        .context("send Lighter keepalive")?;
                    continue;
                }
                message = read.next() => message,
            };
            let Some(message) = message else {
                return Ok(());
            };
            let Message::Text(text) = message.context("read Lighter websocket message")? else {
                continue;
            };
            let text = text.as_str();

            let frame = match parse_frame(text) {
                Ok(frame) => frame,
                Err(err) => {
                    // A malformed non-order-book frame is dropped; the stream continues. Order-book
                    // integrity is load-bearing, so a bad book frame surfaces to force a reconnect.
                    warn!(%err, "dropping malformed Lighter frame");
                    continue;
                }
            };

            let Some((kind, symbol, source_timestamp_ms, source_sequence, block_number)) =
                self.route(&frame, &market_ids, &mut books)?
            else {
                continue;
            };

            let mut event = RawMarketEvent::from_wire(
                "lighter",
                CONNECTOR_VERSION,
                kind,
                text.as_bytes().to_vec(),
            )?;
            event.symbol = symbol;
            event.source_timestamp_ms = source_timestamp_ms;
            event.source_sequence = source_sequence;
            if kind == MarketEventKind::ChainBlock {
                event.block_number = block_number;
                event.finality = Finality::Confirmed;
            }
            handle(event).await?;
        }
    }

    /// Resolve a decoded frame to the event metadata to emit, advancing book state. Returns `None`
    /// for frames that are validated but not persisted (acknowledgements, unknown types).
    #[allow(clippy::type_complexity)]
    fn route(
        &self,
        frame: &Frame,
        market_ids: &HashMap<u64, String>,
        books: &mut HashMap<String, OrderBook>,
    ) -> anyhow::Result<
        Option<(
            MarketEventKind,
            Option<String>,
            Option<i64>,
            Option<String>,
            Option<i64>,
        )>,
    > {
        let resolved = match frame {
            Frame::OrderBook(frame) => {
                let book = books.entry(frame.channel.clone()).or_default();
                book.apply(&frame.channel, &frame.order_book)?;
                (
                    MarketEventKind::OrderBook,
                    channel_market_id(&frame.channel).and_then(|id| market_ids.get(&id).cloned()),
                    Some(frame.timestamp_ms),
                    Some(frame.order_book.nonce.to_string()),
                    None,
                )
            }
            Frame::Ticker(frame) => (
                MarketEventKind::Ticker,
                Some(frame.ticker.symbol.clone()),
                Some(frame.timestamp_ms),
                Some(frame.nonce.to_string()),
                None,
            ),
            Frame::Trade(frame) => (
                MarketEventKind::Trade,
                channel_market_id(&frame.channel).and_then(|id| market_ids.get(&id).cloned()),
                frame
                    .trades
                    .first()
                    .or_else(|| frame.liquidation_trades.first())
                    .map(|trade| trade.timestamp),
                Some(frame.nonce.to_string()),
                None,
            ),
            Frame::MarketStats(frame) => (
                MarketEventKind::MarketStats,
                Some(frame.market_stats.symbol.clone()),
                Some(frame.timestamp_ms),
                None,
                None,
            ),
            Frame::Height(frame) => (
                MarketEventKind::ChainBlock,
                None,
                Some(frame.timestamp_ms),
                None,
                Some(frame.height),
            ),
            Frame::Ack(_) | Frame::Other(_) => return Ok(None),
        };
        Ok(Some(resolved))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn subscription_budget_allows_the_documented_maximum() {
        assert_eq!(subscription_budget(24).unwrap(), 97);
    }

    #[test]
    fn subscription_budget_rejects_overflow() {
        let err = subscription_budget(25).unwrap_err();
        assert!(matches!(
            err,
            LighterError::SubscriptionBudget {
                requested: 101,
                limit: 100
            }
        ));
    }

    #[test]
    fn backoff_is_capped_and_jittered() {
        assert_eq!(backoff_ceiling_ms(0), BASE_BACKOFF_MS);
        assert_eq!(backoff_ceiling_ms(100), MAX_BACKOFF_MS);
        assert!(backoff_ceiling_ms(1) > backoff_ceiling_ms(0));
        let delay = reconnect_delay_ms(3, 12345);
        let ceiling = backoff_ceiling_ms(3);
        assert!(delay >= ceiling / 2 && delay <= ceiling);
    }

    #[test]
    fn channel_market_id_reads_trailing_index() {
        assert_eq!(channel_market_id("order_book:12"), Some(12));
        assert_eq!(channel_market_id("height"), None);
    }
}
