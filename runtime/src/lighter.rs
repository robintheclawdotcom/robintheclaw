//! Read-only Lighter public market-data connector.

use crate::{Finality, MarketEventKind, RawMarketEvent, SourceIdentity};
use anyhow::Context;
use futures_util::{SinkExt, StreamExt};
use serde::de::DeserializeOwned;
use serde::Deserialize;
use serde_json::json;
use std::collections::{HashMap, HashSet};
use std::fmt;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::time::{interval, sleep, timeout, MissedTickBehavior};
use tokio_tungstenite::{connect_async, tungstenite::Message};
use tracing::{info, warn};
use uuid::Uuid;

const CONNECTOR_VERSION: &str = env!("CARGO_PKG_VERSION");

pub const CHANNELS_PER_MARKET: usize = 4;
pub const MAX_SUBSCRIPTIONS: usize = 100;

const KEEPALIVE: Duration = Duration::from_secs(60);
const CONNECT_TIMEOUT: Duration = Duration::from_secs(10);
const BASE_BACKOFF_MS: u64 = 500;
const MAX_BACKOFF_MS: u64 = 30_000;

#[derive(Debug)]
pub enum LighterError {
    Decode(String),
    ContinuityGap {
        channel: String,
        expected: u64,
        received: u64,
    },
    Acknowledgement(String),
    SubscriptionBudget {
        requested: usize,
        limit: usize,
    },
    Protocol(String),
    Server {
        code: Option<String>,
        message: String,
    },
}

impl LighterError {
    pub fn requires_reconnect(&self) -> bool {
        !matches!(self, LighterError::SubscriptionBudget { .. })
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
            LighterError::Protocol(detail) => write!(f, "invalid Lighter protocol state: {detail}"),
            LighterError::Server { code, message } => match code {
                Some(code) => write!(f, "Lighter server error {code}: {message}"),
                None => write!(f, "Lighter server error: {message}"),
            },
        }
    }
}

impl std::error::Error for LighterError {}

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
    #[serde(rename = "last_updated_at")]
    pub last_updated_us: u64,
    pub offset: u64,
    pub order_book: OrderBookData,
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
    pub current_funding_rate: String,
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
    #[serde(rename = "type")]
    pub frame_type: String,
    pub channel: String,
    #[serde(rename = "timestamp", default)]
    pub timestamp_ms: Option<i64>,
}

#[derive(Debug, Deserialize)]
pub struct ErrorFrame {
    #[serde(rename = "type")]
    pub frame_type: String,
    #[serde(default)]
    pub code: Option<serde_json::Value>,
    #[serde(alias = "error")]
    pub message: String,
}

#[derive(Debug)]
pub enum Frame {
    OrderBook(Box<OrderBookFrame>),
    Ticker(Box<TickerFrame>),
    Trade(Box<TradeFrame>),
    MarketStats(Box<MarketStatsFrame>),
    Height(HeightFrame),
    Ack(AckFrame),
    Error(ErrorFrame),
    Other(String),
}

fn decode<T: DeserializeOwned>(text: &str) -> Result<T, LighterError> {
    serde_json::from_str(text).map_err(|err| LighterError::Decode(err.to_string()))
}

pub fn parse_frame(text: &str) -> Result<Frame, LighterError> {
    let envelope: Envelope = decode(text)?;
    let frame = match envelope.frame_type.as_str() {
        "update/order_book" => Frame::OrderBook(Box::new(decode(text)?)),
        "update/ticker" => Frame::Ticker(Box::new(decode(text)?)),
        "update/trade" => Frame::Trade(Box::new(decode(text)?)),
        "update/market_stats" => Frame::MarketStats(Box::new(decode(text)?)),
        "update/height" => Frame::Height(decode(text)?),
        kind if kind.starts_with("subscribed/") => parse_subscription_frame(text)?,
        kind if kind == "error" || kind.starts_with("error/") || kind.ends_with("/error") => {
            Frame::Error(decode(text)?)
        }
        other => Frame::Other(other.to_string()),
    };
    Ok(frame)
}

pub fn validate_ack(text: &str) -> Result<AckFrame, LighterError> {
    let ack: AckFrame =
        serde_json::from_str(text).map_err(|err| LighterError::Acknowledgement(err.to_string()))?;
    if ack.channel.trim().is_empty() {
        return Err(LighterError::Acknowledgement("empty channel".to_string()));
    }
    let channel_kind = ack.channel.split(':').next().unwrap_or_default();
    if ack.frame_type.strip_prefix("subscribed/") != Some(channel_kind) {
        return Err(LighterError::Acknowledgement(
            "type does not match channel".to_string(),
        ));
    }
    Ok(ack)
}

fn parse_subscription_frame(text: &str) -> Result<Frame, LighterError> {
    let ack = validate_ack(text)?;
    let value: serde_json::Value =
        serde_json::from_str(text).map_err(|err| LighterError::Acknowledgement(err.to_string()))?;
    let frame = match ack.frame_type.strip_prefix("subscribed/") {
        Some("order_book") if value.get("order_book").is_some() => {
            Frame::OrderBook(Box::new(decode(text)?))
        }
        Some("ticker") if value.get("ticker").is_some() => Frame::Ticker(Box::new(decode(text)?)),
        Some("trade") if value.get("trades").is_some() => Frame::Trade(Box::new(decode(text)?)),
        Some("market_stats") if value.get("market_stats").is_some() => {
            Frame::MarketStats(Box::new(decode(text)?))
        }
        Some("height") if value.get("height").is_some() => Frame::Height(decode(text)?),
        _ => Frame::Ack(ack),
    };
    Ok(frame)
}

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

#[derive(Debug)]
pub struct SubscriptionState {
    requested: HashSet<String>,
    active: HashSet<String>,
}

impl SubscriptionState {
    pub fn new(market_ids: impl IntoIterator<Item = u64>) -> Self {
        let mut requested = HashSet::from(["height".to_string()]);
        for market_id in market_ids {
            for channel in ["order_book", "ticker", "trade", "market_stats"] {
                requested.insert(format!("{channel}:{market_id}"));
            }
        }
        Self {
            requested,
            active: HashSet::new(),
        }
    }

    pub fn observe(&mut self, frame: &Frame) -> Result<(), LighterError> {
        let Some((channel, expected_kind)) = frame_channel(frame) else {
            return Ok(());
        };
        if channel.split(':').next() != Some(expected_kind) {
            return Err(LighterError::Protocol(format!(
                "{expected_kind} frame carried channel {channel}"
            )));
        }
        if !self.requested.contains(channel) {
            return Err(LighterError::Acknowledgement(format!(
                "unexpected channel {channel}"
            )));
        }
        if matches!(frame, Frame::Ack(_)) {
            if !self.active.insert(channel.to_string()) {
                return Err(LighterError::Acknowledgement(format!(
                    "duplicate acknowledgement for {channel}"
                )));
            }
        } else {
            self.active.insert(channel.to_string());
        }
        Ok(())
    }

    pub fn is_active(&self, channel: &str) -> bool {
        self.active.contains(channel)
    }
}

fn frame_channel(frame: &Frame) -> Option<(&str, &str)> {
    match frame {
        Frame::OrderBook(frame) => Some((&frame.channel, "order_book")),
        Frame::Ticker(frame) => Some((&frame.channel, "ticker")),
        Frame::Trade(frame) => Some((&frame.channel, "trade")),
        Frame::MarketStats(frame) => Some((&frame.channel, "market_stats")),
        Frame::Height(frame) => Some((&frame.channel, "height")),
        Frame::Ack(frame) => Some((
            &frame.channel,
            frame
                .frame_type
                .strip_prefix("subscribed/")
                .unwrap_or_default(),
        )),
        Frame::Error(_) | Frame::Other(_) => None,
    }
}

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

    pub fn apply(&mut self, channel: &str, data: &OrderBookData) -> Result<(), LighterError> {
        let result = self.apply_validated(channel, data);
        if result.is_err() {
            self.invalidate();
        }
        result
    }

    fn apply_validated(&mut self, channel: &str, data: &OrderBookData) -> Result<(), LighterError> {
        if data.code != 0 {
            return Err(LighterError::Protocol(format!(
                "order book {channel} returned code {}",
                data.code
            )));
        }
        validate_levels(&data.bids)?;
        validate_levels(&data.asks)?;

        match self.nonce {
            None => {
                validate_nonce_range(channel, data)?;
                self.bids.clear();
                self.asks.clear();
                write_levels(&mut self.bids, &data.bids)?;
                write_levels(&mut self.asks, &data.asks)?;
                self.nonce = Some(data.nonce);
                Ok(())
            }
            Some(current) => {
                if data.begin_nonce != current {
                    return Err(LighterError::ContinuityGap {
                        channel: channel.to_string(),
                        expected: current,
                        received: data.begin_nonce,
                    });
                }
                validate_nonce_range(channel, data)?;
                write_levels(&mut self.bids, &data.bids)?;
                write_levels(&mut self.asks, &data.asks)?;
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

fn validate_nonce_range(channel: &str, data: &OrderBookData) -> Result<(), LighterError> {
    if data.nonce < data.begin_nonce {
        return Err(LighterError::Protocol(format!(
            "order book {channel} nonce {} precedes begin_nonce {}",
            data.nonce, data.begin_nonce
        )));
    }
    Ok(())
}

fn validate_levels(levels: &[Level]) -> Result<(), LighterError> {
    for level in levels {
        if decimal_is_zero(&level.price)? {
            return Err(LighterError::Protocol(
                "order book price must be positive".to_string(),
            ));
        }
        decimal_is_zero(&level.size)?;
    }
    Ok(())
}

fn write_levels(book: &mut HashMap<String, String>, levels: &[Level]) -> Result<(), LighterError> {
    for level in levels {
        if decimal_is_zero(&level.size)? {
            book.remove(&level.price);
        } else {
            book.insert(level.price.clone(), level.size.clone());
        }
    }
    Ok(())
}

fn decimal_is_zero(value: &str) -> Result<bool, LighterError> {
    let mut digits = 0usize;
    let mut dots = 0usize;
    let mut zero = true;

    for byte in value.bytes() {
        match byte {
            b'0' => digits += 1,
            b'1'..=b'9' => {
                digits += 1;
                zero = false;
            }
            b'.' if dots == 0 && digits > 0 => dots += 1,
            _ => {
                return Err(LighterError::Protocol(format!(
                    "invalid decimal value {value}"
                )))
            }
        }
    }
    if digits == 0 || value.ends_with('.') {
        return Err(LighterError::Protocol(format!(
            "invalid decimal value {value}"
        )));
    }
    Ok(zero)
}

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
        let mut subscriptions = SubscriptionState::new(market_ids.keys().copied());
        let source_session = Uuid::new_v4().to_string();
        let mut wire_sequence = 0u64;

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
            wire_sequence = wire_sequence
                .checked_add(1)
                .ok_or_else(|| LighterError::Protocol("frame sequence overflow".to_string()))?;

            let frame = match parse_frame(text) {
                Ok(frame) => frame,
                Err(err) => {
                    if let Ok(identity) = SourceIdentity::new(
                        source_session.clone(),
                        format!("frame:{wire_sequence}"),
                        None,
                    ) {
                        if let Ok(event) = RawMarketEvent::from_source(
                            "lighter",
                            CONNECTOR_VERSION,
                            identity,
                            MarketEventKind::SourceHealth,
                            text.as_bytes().to_vec(),
                        ) {
                            handle(event).await?;
                        }
                    }
                    return Err(err.into());
                }
            };

            if let Err(err) = subscriptions.observe(&frame) {
                let identity = SourceIdentity::new(
                    source_session.clone(),
                    format!("frame:{wire_sequence}"),
                    None,
                )?;
                let event = RawMarketEvent::from_source(
                    "lighter",
                    CONNECTOR_VERSION,
                    identity,
                    MarketEventKind::SourceHealth,
                    text.as_bytes().to_vec(),
                )?;
                handle(event).await?;
                return Err(err.into());
            }

            let routed = match self.route(&frame, &market_ids, &mut books) {
                Ok(routed) => routed,
                Err(err) => {
                    let identity = SourceIdentity::new(
                        source_session.clone(),
                        format!("frame:{wire_sequence}"),
                        None,
                    )?;
                    let event = RawMarketEvent::from_source(
                        "lighter",
                        CONNECTOR_VERSION,
                        identity,
                        MarketEventKind::SourceHealth,
                        text.as_bytes().to_vec(),
                    )?;
                    handle(event).await?;
                    return Err(err);
                }
            };
            let identity = SourceIdentity::new(
                source_session.clone(),
                format!("frame:{wire_sequence}"),
                routed.source_sequence.clone(),
            )?;
            let mut event = RawMarketEvent::from_source(
                "lighter",
                CONNECTOR_VERSION,
                identity,
                routed.kind,
                text.as_bytes().to_vec(),
            )?;
            event.symbol = routed.symbol;
            event.source_timestamp_ms = routed.source_timestamp_ms;
            if routed.kind == MarketEventKind::ChainBlock {
                event.block_number = routed.block_number;
                event.finality = Finality::Confirmed;
            }
            handle(event).await?;

            if let Frame::Error(frame) = frame {
                return Err(LighterError::Server {
                    code: frame.code.map(|code| match code {
                        serde_json::Value::String(value) => value,
                        value => value.to_string(),
                    }),
                    message: frame.message,
                }
                .into());
            }
        }
    }

    fn route(
        &self,
        frame: &Frame,
        market_ids: &HashMap<u64, String>,
        books: &mut HashMap<String, OrderBook>,
    ) -> anyhow::Result<RoutedFrame> {
        let resolved = match frame {
            Frame::OrderBook(frame) => {
                let book = books.entry(frame.channel.clone()).or_default();
                book.apply(&frame.channel, &frame.order_book)?;
                RoutedFrame::new(MarketEventKind::OrderBook)
                    .symbol(
                        channel_market_id(&frame.channel)
                            .and_then(|id| market_ids.get(&id).cloned()),
                    )
                    .timestamp(frame.timestamp_ms)
                    .sequence(frame.order_book.nonce)
            }
            Frame::Ticker(frame) => RoutedFrame::new(MarketEventKind::Ticker)
                .symbol(Some(frame.ticker.symbol.clone()))
                .timestamp(frame.timestamp_ms)
                .sequence(frame.nonce),
            Frame::Trade(frame) => RoutedFrame::new(MarketEventKind::Trade)
                .symbol(
                    channel_market_id(&frame.channel).and_then(|id| market_ids.get(&id).cloned()),
                )
                .optional_timestamp(
                    frame
                        .trades
                        .first()
                        .or_else(|| frame.liquidation_trades.first())
                        .map(|trade| trade.timestamp),
                )
                .sequence(frame.nonce),
            Frame::MarketStats(frame) => RoutedFrame::new(MarketEventKind::MarketStats)
                .symbol(Some(frame.market_stats.symbol.clone()))
                .timestamp(frame.timestamp_ms)
                .sequence(frame.timestamp_ms),
            Frame::Height(frame) => RoutedFrame::new(MarketEventKind::ChainBlock)
                .timestamp(frame.timestamp_ms)
                .sequence(frame.height)
                .block(frame.height),
            Frame::Ack(frame) => RoutedFrame::new(MarketEventKind::SourceHealth)
                .optional_timestamp(frame.timestamp_ms),
            Frame::Error(_) | Frame::Other(_) => RoutedFrame::new(MarketEventKind::SourceHealth),
        };
        Ok(resolved)
    }
}

struct RoutedFrame {
    kind: MarketEventKind,
    symbol: Option<String>,
    source_timestamp_ms: Option<i64>,
    source_sequence: Option<String>,
    block_number: Option<i64>,
}

impl RoutedFrame {
    fn new(kind: MarketEventKind) -> Self {
        Self {
            kind,
            symbol: None,
            source_timestamp_ms: None,
            source_sequence: None,
            block_number: None,
        }
    }

    fn symbol(mut self, symbol: Option<String>) -> Self {
        self.symbol = symbol;
        self
    }

    fn timestamp(mut self, timestamp: i64) -> Self {
        self.source_timestamp_ms = Some(timestamp);
        self
    }

    fn optional_timestamp(mut self, timestamp: Option<i64>) -> Self {
        self.source_timestamp_ms = timestamp;
        self
    }

    fn sequence(mut self, sequence: impl ToString) -> Self {
        self.source_sequence = Some(sequence.to_string());
        self
    }

    fn block(mut self, block: i64) -> Self {
        self.block_number = Some(block);
        self
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
