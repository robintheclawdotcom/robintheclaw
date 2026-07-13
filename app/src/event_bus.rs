//! Platform event bus for the basis-arb lifecycle. Events fan out over a tokio broadcast channel;
//! consumers (the live feed hub, future webhooks or persistence) subscribe independently. A slow
//! consumer loses the oldest events rather than stalling the producer.

use chrono::{DateTime, Utc};
use log::warn;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use tokio::sync::broadcast;

const EVENT_BUS_CAPACITY: usize = 2048;

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "event", content = "data")]
pub enum PlatformEvent {
    #[serde(rename = "basis_observed")]
    BasisObserved(BasisObservedEvent),
    #[serde(rename = "trade_planned")]
    TradePlanned(TradePlannedEvent),
    #[serde(rename = "leg_filled")]
    LegFilled(LegFilledEvent),
    #[serde(rename = "position_closed")]
    PositionClosed(PositionClosedEvent),
    #[serde(rename = "agent_halted")]
    AgentHalted(AgentHaltedEvent),
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BasisObservedEvent {
    pub symbol: String,
    pub spot_price: f64,
    pub perp_mark: f64,
    pub basis_bps: f64,
    pub timestamp: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TradePlannedEvent {
    pub symbol: String,
    pub direction: String,
    pub notional_usd: f64,
    pub basis_bps: f64,
    pub timestamp: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LegFilledEvent {
    pub symbol: String,
    pub venue: String,
    pub side: String,
    pub qty: f64,
    pub price: f64,
    pub timestamp: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PositionClosedEvent {
    pub symbol: String,
    pub realized_pnl_usd: f64,
    pub timestamp: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AgentHaltedEvent {
    pub reason: String,
    pub metadata: Value,
    pub timestamp: DateTime<Utc>,
}

pub struct EventBus {
    tx: broadcast::Sender<PlatformEvent>,
}

impl EventBus {
    pub fn new() -> Self {
        let (tx, _) = broadcast::channel(EVENT_BUS_CAPACITY);
        Self { tx }
    }

    /// Publish an event. A send with no subscribers is a no-op, not an error.
    pub fn emit(&self, event: PlatformEvent) {
        if self.tx.receiver_count() > 0 {
            if let Err(e) = self.tx.send(event) {
                warn!("event bus send failed (lagged receivers): {e}");
            }
        }
    }

    pub fn subscribe(&self) -> broadcast::Receiver<PlatformEvent> {
        self.tx.subscribe()
    }
}

impl Default for EventBus {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn event_round_trips_through_json() {
        let ev = PlatformEvent::BasisObserved(BasisObservedEvent {
            symbol: "NVDA".into(),
            spot_price: 212.34,
            perp_mark: 212.19,
            basis_bps: -7.1,
            timestamp: Utc::now(),
        });
        let s = serde_json::to_string(&ev).unwrap();
        assert!(s.contains("basis_observed"));
        let back: PlatformEvent = serde_json::from_str(&s).unwrap();
        match back {
            PlatformEvent::BasisObserved(e) => assert_eq!(e.symbol, "NVDA"),
            _ => panic!("wrong variant"),
        }
    }
}
