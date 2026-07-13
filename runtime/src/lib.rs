use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::collections::{HashSet, VecDeque};
use uuid::Uuid;

pub mod archive;
pub mod chain;
pub mod lighter;
pub mod paper;
pub mod storage;

pub const EVENT_SCHEMA_VERSION: &str = "1";

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum MarketEventKind {
    OrderBook,
    Ticker,
    Trade,
    Funding,
    OpenInterest,
    MarketStats,
    ChainBlock,
    Sequencer,
    PoolState,
    SourceHealth,
}

impl MarketEventKind {
    pub fn as_db(self) -> &'static str {
        match self {
            Self::OrderBook => "order_book",
            Self::Ticker => "ticker",
            Self::Trade => "trade",
            Self::Funding => "funding",
            Self::OpenInterest => "open_interest",
            Self::MarketStats => "market_stats",
            Self::ChainBlock => "chain_block",
            Self::Sequencer => "sequencer",
            Self::PoolState => "pool_state",
            Self::SourceHealth => "source_health",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Finality {
    Pending,
    Confirmed,
    L1Posted,
    Finalized,
    NotApplicable,
}

impl Finality {
    pub fn as_db(self) -> &'static str {
        match self {
            Self::Pending => "pending",
            Self::Confirmed => "confirmed",
            Self::L1Posted => "l1_posted",
            Self::Finalized => "finalized",
            Self::NotApplicable => "not_applicable",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum CanonicalState {
    Canonical,
    Orphaned,
    NotApplicable,
}

impl CanonicalState {
    pub fn as_db(self) -> &'static str {
        match self {
            Self::Canonical => "canonical",
            Self::Orphaned => "orphaned",
            Self::NotApplicable => "not_applicable",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct SourceIdentity {
    pub session: String,
    pub event_id: String,
    pub sequence: Option<String>,
}

impl SourceIdentity {
    pub fn new(
        session: impl Into<String>,
        event_id: impl Into<String>,
        sequence: Option<String>,
    ) -> anyhow::Result<Self> {
        let identity = Self {
            session: session.into(),
            event_id: event_id.into(),
            sequence,
        };
        identity.validate()?;
        Ok(identity)
    }

    fn validate(&self) -> anyhow::Result<()> {
        for (name, value) in [("session", &self.session), ("event_id", &self.event_id)] {
            anyhow::ensure!(!value.is_empty(), "source {name} cannot be empty");
            anyhow::ensure!(value.len() <= 256, "source {name} exceeds 256 bytes");
            anyhow::ensure!(
                !value.chars().any(char::is_control),
                "source {name} contains control characters"
            );
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RawMarketEvent {
    pub id: Uuid,
    pub schema_version: String,
    pub source: String,
    pub source_session: String,
    pub source_event_id: String,
    pub connector_version: String,
    pub kind: MarketEventKind,
    pub symbol: Option<String>,
    pub source_timestamp_ms: Option<i64>,
    pub received_at: DateTime<Utc>,
    pub source_sequence: Option<String>,
    pub block_number: Option<i64>,
    pub block_hash: Option<String>,
    pub parent_block_hash: Option<String>,
    pub canonical_state: CanonicalState,
    pub finality: Finality,
    pub payload_sha256: String,
    pub payload: Value,
    pub raw: Vec<u8>,
}

impl RawMarketEvent {
    pub fn from_source(
        source: impl Into<String>,
        connector_version: impl Into<String>,
        identity: SourceIdentity,
        kind: MarketEventKind,
        raw: impl Into<Vec<u8>>,
    ) -> anyhow::Result<Self> {
        identity.validate()?;
        let raw = raw.into();
        let payload = serde_json::from_slice(&raw)?;
        let payload_sha256 = sha256(&raw);
        Ok(Self {
            id: Uuid::new_v4(),
            schema_version: EVENT_SCHEMA_VERSION.to_string(),
            source: source.into(),
            source_session: identity.session,
            source_event_id: identity.event_id,
            connector_version: connector_version.into(),
            kind,
            symbol: None,
            source_timestamp_ms: None,
            received_at: Utc::now(),
            source_sequence: identity.sequence,
            block_number: None,
            block_hash: None,
            parent_block_hash: None,
            canonical_state: CanonicalState::NotApplicable,
            finality: Finality::NotApplicable,
            payload_sha256,
            payload,
            raw,
        })
    }

    pub fn from_wire(
        source: impl Into<String>,
        connector_version: impl Into<String>,
        kind: MarketEventKind,
        raw: impl Into<Vec<u8>>,
    ) -> anyhow::Result<Self> {
        let raw = raw.into();
        let digest = sha256(&raw);
        Self::from_source(
            source,
            connector_version,
            SourceIdentity::new("legacy", format!("payload:{digest}"), None)?,
            kind,
            raw,
        )
    }
}

pub fn sha256(bytes: &[u8]) -> String {
    let mut hash = Sha256::new();
    hash.update(bytes);
    hex::encode(hash.finalize())
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ShadowStatus {
    Declined,
    Proposed,
    PartiallyHedged,
    Hedged,
    Unhedged,
    Cancelled,
    Expired,
    Unwound,
    Stale,
}

impl ShadowStatus {
    pub fn as_db(self) -> &'static str {
        match self {
            Self::Declined => "declined",
            Self::Proposed => "proposed",
            Self::PartiallyHedged => "partially_hedged",
            Self::Hedged => "hedged",
            Self::Unhedged => "unhedged",
            Self::Cancelled => "cancelled",
            Self::Expired => "expired",
            Self::Unwound => "unwound",
            Self::Stale => "stale",
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Quote {
    pub bid: f64,
    pub bid_size: f64,
    pub ask: f64,
    pub ask_size: f64,
    pub observed_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ShadowConfig {
    pub requested_notional_usd: f64,
    pub minimum_net_edge_bps: f64,
    pub spot_fee_bps: f64,
    pub perp_fee_bps: f64,
    pub fixed_gas_usd: f64,
    pub max_quote_age_ms: i64,
    pub max_impact_bps: f64,
}

impl Default for ShadowConfig {
    fn default() -> Self {
        Self {
            requested_notional_usd: 25.0,
            minimum_net_edge_bps: 12.0,
            spot_fee_bps: 30.0,
            perp_fee_bps: 5.0,
            fixed_gas_usd: 0.05,
            max_quote_age_ms: 1_000,
            max_impact_bps: 50.0,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ShadowDirection {
    LongSpotShortPerp,
    ShortSpotLongPerp,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ShadowLeg {
    pub venue: String,
    pub side: String,
    pub requested_notional_usd: f64,
    pub filled_notional_usd: f64,
    pub price: f64,
    pub fee_usd: f64,
    pub impact_bps: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ShadowDecision {
    pub status: ShadowStatus,
    pub direction: Option<ShadowDirection>,
    pub gross_edge_bps: f64,
    pub net_edge_bps: f64,
    pub spot: Option<ShadowLeg>,
    pub perp: Option<ShadowLeg>,
    pub reason: Option<String>,
}

fn finite_positive(value: f64) -> bool {
    value.is_finite() && value > 0.0
}

fn quote_age_ms(now: DateTime<Utc>, quote: &Quote) -> i64 {
    now.signed_duration_since(quote.observed_at)
        .num_milliseconds()
        .max(0)
}

fn impact_bps(notional: f64, displayed_notional: f64) -> f64 {
    if displayed_notional <= 0.0 {
        return f64::INFINITY;
    }
    (notional / displayed_notional) * 10_000.0
}

/// Simulates a paired taker trade using the executable best prices that were visible at the
/// decision event. It deliberately declines rather than inventing a fill when either leg lacks
/// contemporaneous depth.
pub fn shadow_pair(
    now: DateTime<Utc>,
    spot: &Quote,
    perp: &Quote,
    config: &ShadowConfig,
) -> ShadowDecision {
    let quote_valid = [
        spot.bid,
        spot.bid_size,
        spot.ask,
        spot.ask_size,
        perp.bid,
        perp.bid_size,
        perp.ask,
        perp.ask_size,
        config.requested_notional_usd,
    ]
    .into_iter()
    .all(finite_positive);
    let cost_model_valid = [
        config.minimum_net_edge_bps,
        config.spot_fee_bps,
        config.perp_fee_bps,
        config.fixed_gas_usd,
        config.max_impact_bps,
    ]
    .into_iter()
    .all(|value| value.is_finite() && value >= 0.0);
    if !quote_valid || !cost_model_valid || config.max_quote_age_ms < 0 {
        return declined("invalid quote or shadow configuration");
    }

    if quote_age_ms(now, spot) > config.max_quote_age_ms
        || quote_age_ms(now, perp) > config.max_quote_age_ms
    {
        return ShadowDecision {
            status: ShadowStatus::Stale,
            direction: None,
            gross_edge_bps: 0.0,
            net_edge_bps: 0.0,
            spot: None,
            perp: None,
            reason: Some("spot or perp quote exceeded maximum age".to_string()),
        };
    }

    let rich = (perp.bid - spot.ask) / spot.ask * 10_000.0;
    let cheap = (spot.bid - perp.ask) / perp.ask * 10_000.0;
    let (
        direction,
        gross_edge_bps,
        spot_side,
        spot_price,
        spot_size,
        perp_side,
        perp_price,
        perp_size,
    ) = if rich >= cheap {
        (
            ShadowDirection::LongSpotShortPerp,
            rich,
            "buy",
            spot.ask,
            spot.ask_size,
            "sell",
            perp.bid,
            perp.bid_size,
        )
    } else {
        (
            ShadowDirection::ShortSpotLongPerp,
            cheap,
            "sell",
            spot.bid,
            spot.bid_size,
            "buy",
            perp.ask,
            perp.ask_size,
        )
    };

    let requested = config.requested_notional_usd;
    let spot_impact = impact_bps(requested, spot_price * spot_size);
    let perp_impact = impact_bps(requested, perp_price * perp_size);
    if spot_impact > config.max_impact_bps || perp_impact > config.max_impact_bps {
        return declined("displayed depth cannot support the requested paired notional");
    }

    let spot_fee_usd = requested * config.spot_fee_bps / 10_000.0;
    let perp_fee_usd = requested * config.perp_fee_bps / 10_000.0;
    let net_edge_bps = gross_edge_bps
        - config.spot_fee_bps
        - config.perp_fee_bps
        - spot_impact
        - perp_impact
        - (config.fixed_gas_usd / requested * 10_000.0);
    if net_edge_bps < config.minimum_net_edge_bps {
        return declined("gross basis did not survive modeled execution costs");
    }

    let fill_ratio =
        ((spot_price * spot_size).min(perp_price * perp_size) / requested).clamp(0.0, 1.0);
    let filled = requested * fill_ratio;
    let status = if fill_ratio >= 0.999 {
        ShadowStatus::Hedged
    } else if fill_ratio > 0.0 {
        ShadowStatus::PartiallyHedged
    } else {
        ShadowStatus::Unhedged
    };

    ShadowDecision {
        status,
        direction: Some(direction),
        gross_edge_bps,
        net_edge_bps,
        spot: Some(ShadowLeg {
            venue: "spot".to_string(),
            side: spot_side.to_string(),
            requested_notional_usd: requested,
            filled_notional_usd: filled,
            price: spot_price,
            fee_usd: spot_fee_usd,
            impact_bps: spot_impact,
        }),
        perp: Some(ShadowLeg {
            venue: "perp".to_string(),
            side: perp_side.to_string(),
            requested_notional_usd: requested,
            filled_notional_usd: filled,
            price: perp_price,
            fee_usd: perp_fee_usd,
            impact_bps: perp_impact,
        }),
        reason: None,
    }
}

fn declined(reason: &str) -> ShadowDecision {
    ShadowDecision {
        status: ShadowStatus::Declined,
        direction: None,
        gross_edge_bps: 0.0,
        net_edge_bps: 0.0,
        spot: None,
        perp: None,
        reason: Some(reason.to_string()),
    }
}

pub struct IntentDeduper {
    seen: HashSet<String>,
    order: VecDeque<String>,
    capacity: usize,
}

impl Default for IntentDeduper {
    fn default() -> Self {
        Self::with_capacity(100_000)
    }
}

impl IntentDeduper {
    pub fn with_capacity(capacity: usize) -> Self {
        Self {
            seen: HashSet::new(),
            order: VecDeque::new(),
            capacity: capacity.max(1),
        }
    }

    pub fn accept(
        &mut self,
        strategy_version: &str,
        event_id: Uuid,
        direction: ShadowDirection,
    ) -> bool {
        let key = format!("{strategy_version}:{event_id}:{direction:?}");
        if !self.seen.insert(key.clone()) {
            return false;
        }
        self.order.push_back(key);
        if self.order.len() > self.capacity {
            if let Some(expired) = self.order.pop_front() {
                self.seen.remove(&expired);
            }
        }
        true
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::Duration;

    fn quote(bid: f64, ask: f64) -> Quote {
        Quote {
            bid,
            bid_size: 100.0,
            ask,
            ask_size: 100.0,
            observed_at: Utc::now(),
        }
    }

    #[test]
    fn fresh_net_profitable_basis_is_hedged() {
        let result = shadow_pair(
            Utc::now(),
            &quote(100.0, 100.1),
            &quote(102.1, 102.2),
            &ShadowConfig::default(),
        );
        assert_eq!(result.status, ShadowStatus::Hedged);
        assert_eq!(result.direction, Some(ShadowDirection::LongSpotShortPerp));
    }

    #[test]
    fn stale_quotes_cannot_create_an_intent() {
        let mut spot = quote(100.0, 100.1);
        spot.observed_at -= Duration::seconds(2);
        assert_eq!(
            shadow_pair(
                Utc::now(),
                &spot,
                &quote(101.1, 101.2),
                &ShadowConfig::default()
            )
            .status,
            ShadowStatus::Stale
        );
    }

    #[test]
    fn deduper_accepts_each_event_once() {
        let mut deduper = IntentDeduper::default();
        let event = Uuid::new_v4();
        assert!(deduper.accept("baseline-1", event, ShadowDirection::LongSpotShortPerp));
        assert!(!deduper.accept("baseline-1", event, ShadowDirection::LongSpotShortPerp));
    }

    #[test]
    fn deduper_bounds_memory() {
        let mut deduper = IntentDeduper::with_capacity(1);
        let first = Uuid::new_v4();
        let second = Uuid::new_v4();
        assert!(deduper.accept("baseline-1", first, ShadowDirection::LongSpotShortPerp));
        assert!(deduper.accept("baseline-1", second, ShadowDirection::LongSpotShortPerp));
        assert!(deduper.accept("baseline-1", first, ShadowDirection::LongSpotShortPerp));
    }

    #[test]
    fn zero_fee_configuration_stays_finite() {
        let result = shadow_pair(
            Utc::now(),
            &quote(100.0, 100.1),
            &quote(102.1, 102.2),
            &ShadowConfig {
                spot_fee_bps: 0.0,
                perp_fee_bps: 0.0,
                fixed_gas_usd: 0.0,
                ..ShadowConfig::default()
            },
        );
        assert!(result.spot.unwrap().fee_usd.is_finite());
        assert!(result.perp.unwrap().fee_usd.is_finite());
    }

    #[test]
    fn source_identity_rejects_empty_and_control_values() {
        assert!(SourceIdentity::new("", "event-1", None).is_err());
        assert!(SourceIdentity::new("session-1", "event\n1", None).is_err());
        assert!(SourceIdentity::new("session-1", "event-1", Some("7".to_string())).is_ok());
    }
}
