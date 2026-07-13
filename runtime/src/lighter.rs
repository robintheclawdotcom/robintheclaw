use crate::{Finality, MarketEventKind, RawMarketEvent};
use anyhow::Context;
use futures_util::{SinkExt, StreamExt};
use serde_json::{json, Value};
use std::{collections::HashMap, time::Duration};
use tokio::time::{interval, timeout, MissedTickBehavior};
use tokio_tungstenite::{connect_async, tungstenite::Message};
use tracing::{info, warn};

const CONNECTOR_VERSION: &str = env!("CARGO_PKG_VERSION");

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

    async fn market_ids(&self) -> anyhow::Result<HashMap<u64, String>> {
        let url = format!(
            "{}/api/v1/orderBookDetails",
            self.api_url.trim_end_matches('/')
        );
        let body: Value = self
            .client
            .get(url)
            .send()
            .await
            .context("fetch Lighter market metadata")?
            .error_for_status()
            .context("Lighter market metadata response")?
            .json()
            .await
            .context("decode Lighter market metadata")?;
        let wanted = self
            .symbols
            .iter()
            .map(String::as_str)
            .collect::<std::collections::HashSet<_>>();
        let markets = body["order_book_details"]
            .as_array()
            .context("Lighter market metadata has no order_book_details")?;
        let ids = markets
            .iter()
            .filter(|market| {
                market["market_type"].as_str() == Some("perp")
                    && market["status"].as_str() == Some("active")
                    && market["symbol"]
                        .as_str()
                        .is_some_and(|symbol| wanted.contains(symbol))
            })
            .filter_map(|market| {
                Some((
                    market["market_id"].as_u64()?,
                    market["symbol"].as_str()?.to_string(),
                ))
            })
            .collect::<HashMap<_, _>>();
        if ids.is_empty() {
            anyhow::bail!("no configured symbols are active Lighter perp markets");
        }
        Ok(ids)
    }

    pub async fn run<F, Fut>(&self, mut handle: F) -> anyhow::Result<()>
    where
        F: FnMut(RawMarketEvent) -> Fut,
        Fut: std::future::Future<Output = anyhow::Result<()>>,
    {
        let market_ids = self.market_ids().await?;
        let (socket, _) = timeout(Duration::from_secs(10), connect_async(&self.websocket_url))
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

        let mut keepalive = interval(Duration::from_secs(60));
        keepalive.set_missed_tick_behavior(MissedTickBehavior::Skip);
        keepalive.tick().await;
        let mut last_order_book_nonce = HashMap::new();
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
                break;
            };
            let message = message.context("read Lighter websocket message")?;
            let Message::Text(text) = message else {
                continue;
            };
            let raw = text.as_bytes().to_vec();
            let value: Value =
                serde_json::from_str(&text).context("decode Lighter websocket message")?;
            let Some(kind) = event_kind(&value) else {
                continue;
            };
            if kind == MarketEventKind::OrderBook {
                verify_order_book_continuity(&value, &mut last_order_book_nonce)?;
            }
            let mut event = RawMarketEvent::from_wire("lighter", CONNECTOR_VERSION, kind, raw)?;
            event.source_timestamp_ms = publisher_timestamp_ms(&value);
            event.source_sequence = source_sequence(&value);
            event.symbol = symbol(&value, &market_ids);
            if kind == MarketEventKind::ChainBlock {
                event.block_number = value["height"].as_i64();
                event.finality = Finality::Confirmed;
            }
            handle(event).await?;
        }
        warn!("Lighter websocket closed");
        Ok(())
    }
}

fn event_kind(value: &Value) -> Option<MarketEventKind> {
    match value["type"].as_str()? {
        "update/order_book" => Some(MarketEventKind::OrderBook),
        "update/ticker" => Some(MarketEventKind::Ticker),
        "update/trade" => Some(MarketEventKind::Trade),
        "update/market_stats" => Some(MarketEventKind::MarketStats),
        "update/height" => Some(MarketEventKind::ChainBlock),
        _ => None,
    }
}

fn source_sequence(value: &Value) -> Option<String> {
    value["order_book"]["nonce"]
        .as_u64()
        .or_else(|| value["nonce"].as_u64())
        .map(|nonce| nonce.to_string())
}

fn symbol(value: &Value, market_ids: &HashMap<u64, String>) -> Option<String> {
    value["ticker"]["s"]
        .as_str()
        .map(ToOwned::to_owned)
        .or_else(|| {
            value["market_stats"]["symbol"]
                .as_str()
                .map(ToOwned::to_owned)
        })
        .or_else(|| {
            value["channel"]
                .as_str()
                .and_then(|channel| channel.rsplit(':').next())
                .and_then(|id| id.parse::<u64>().ok())
                .and_then(|id| market_ids.get(&id).cloned())
        })
}

fn publisher_timestamp_ms(value: &Value) -> Option<i64> {
    value["timestamp"]
        .as_i64()
        .or_else(|| {
            value["trades"]
                .as_array()?
                .first()?
                .get("timestamp")?
                .as_i64()
        })
        .or_else(|| {
            value["liquidation_trades"]
                .as_array()?
                .first()?
                .get("timestamp")?
                .as_i64()
        })
}

fn verify_order_book_continuity(
    value: &Value,
    previous: &mut HashMap<String, u64>,
) -> anyhow::Result<()> {
    let channel = value["channel"]
        .as_str()
        .context("order book update has no channel")?
        .to_string();
    let nonce = value["order_book"]["nonce"]
        .as_u64()
        .context("order book update has no nonce")?;
    let begin_nonce = value["order_book"]["begin_nonce"]
        .as_u64()
        .context("order book update has no begin_nonce")?;
    if let Some(last_nonce) = previous.get(&channel) {
        if begin_nonce != *last_nonce {
            anyhow::bail!(
                "Lighter order book continuity gap on {channel}: expected begin_nonce {last_nonce}, received {begin_nonce}"
            );
        }
    }
    previous.insert(channel, nonce);
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn identifies_market_events() {
        assert_eq!(
            event_kind(&json!({ "type": "update/order_book" })),
            Some(MarketEventKind::OrderBook)
        );
        assert!(event_kind(&json!({ "type": "subscribed/order_book" })).is_none());
        assert_eq!(
            event_kind(&json!({ "type": "update/market_stats" })),
            Some(MarketEventKind::MarketStats)
        );
    }

    #[test]
    fn resolves_symbol_from_channel_market_id() {
        let markets = HashMap::from([(12, "NVDA".to_string())]);
        assert_eq!(
            symbol(&json!({ "channel": "order_book:12" }), &markets),
            Some("NVDA".to_string())
        );
    }

    #[test]
    fn rejects_order_book_continuity_gaps() {
        let mut previous = HashMap::from([("order_book:12".to_string(), 10)]);
        let event = json!({
            "channel": "order_book:12",
            "order_book": { "nonce": 12, "begin_nonce": 11 }
        });
        assert!(verify_order_book_continuity(&event, &mut previous).is_err());
    }
}
