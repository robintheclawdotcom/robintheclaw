use crate::{RawMarketEvent, ShadowDecision};
use anyhow::Context;
use bytes::Bytes;
use chrono::{DateTime, Utc};
use object_store::{aws::AmazonS3Builder, path::Path, ObjectStore};
use serde_json::json;
use sqlx::{postgres::PgPoolOptions, PgPool};
use std::{env, sync::Arc, time::Duration};
use uuid::Uuid;

#[derive(Clone)]
pub struct Store {
    pool: PgPool,
    objects: Arc<dyn ObjectStore>,
}

pub struct MarketStatsFeature {
    pub observed_at: DateTime<Utc>,
    pub mark: Option<f64>,
    pub index: Option<f64>,
    pub funding_rate: Option<f64>,
    pub open_interest: Option<f64>,
}

impl Store {
    pub async fn from_env() -> anyhow::Result<Self> {
        let database_url = env::var("DATABASE_URL").context("DATABASE_URL is required")?;
        let bucket = env::var("R2_BUCKET").context("R2_BUCKET is required")?;
        let endpoint = env::var("AWS_ENDPOINT_URL").context("AWS_ENDPOINT_URL is required")?;
        let pool = PgPoolOptions::new()
            .max_connections(12)
            .acquire_timeout(Duration::from_secs(10))
            .connect(&database_url)
            .await
            .context("connect to Postgres")?;
        sqlx::migrate!("./migrations")
            .run(&pool)
            .await
            .context("apply runtime migrations")?;

        let store = AmazonS3Builder::from_env()
            .with_bucket_name(bucket)
            .with_endpoint(endpoint)
            .with_virtual_hosted_style_request(false)
            .build()
            .context("configure R2 object store")?;
        Ok(Self {
            pool,
            objects: Arc::new(store),
        })
    }

    pub async fn persist_event(&self, event: &RawMarketEvent) -> anyhow::Result<bool> {
        let object_key = event.object_key();
        let raw = compress_payload(&event.raw)?;
        self.objects
            .put(&Path::from(object_key.as_str()), Bytes::from(raw).into())
            .await
            .context("archive raw market event")?;

        let inserted = sqlx::query_scalar::<_, Uuid>(
            "INSERT INTO raw_market_events (id, source, connector_version, kind, symbol, source_timestamp_ms, received_at, source_sequence, block_number, block_hash, finality, payload_sha256, raw_object_key, payload) \
             VALUES ($1, $2, $3, $4::market_event_kind, $5, $6, $7, $8, $9, $10, $11::finality_state, $12, $13, $14) \
             ON CONFLICT (source, connector_version, payload_sha256) DO NOTHING \
             RETURNING id",
        )
        .bind(event.id)
        .bind(&event.source)
        .bind(&event.connector_version)
        .bind(event.kind.as_db())
        .bind(&event.symbol)
        .bind(event.source_timestamp_ms)
        .bind(event.received_at)
        .bind(&event.source_sequence)
        .bind(event.block_number)
        .bind(&event.block_hash)
        .bind(event.finality.as_db())
        .bind(&event.payload_sha256)
        .bind(object_key)
        .bind(&event.payload)
        .fetch_optional(&self.pool)
        .await
        .context("persist market event")?;

        Ok(inserted.is_some())
    }

    pub async fn update_source_health(
        &self,
        source: &str,
        status: &str,
        last_event_at: Option<DateTime<Utc>>,
        last_error: Option<&str>,
    ) -> anyhow::Result<()> {
        sqlx::query(
            "INSERT INTO source_health (source, status, last_event_at, last_error) VALUES ($1, $2, $3, $4) \
             ON CONFLICT (source) DO UPDATE SET status = EXCLUDED.status, last_event_at = EXCLUDED.last_event_at, last_error = EXCLUDED.last_error, updated_at = now()",
        )
        .bind(source)
        .bind(status)
        .bind(last_event_at)
        .bind(last_error)
        .execute(&self.pool)
        .await
        .context("update source health")?;
        Ok(())
    }

    pub async fn persist_shadow_decision(
        &self,
        strategy_id: Uuid,
        event_id: Uuid,
        symbol: &str,
        dedupe_key: &str,
        decision: &ShadowDecision,
        at: DateTime<Utc>,
    ) -> anyhow::Result<bool> {
        let intent_id = Uuid::new_v4();
        let decision_json = serde_json::to_value(decision)?;
        let inserted = sqlx::query_scalar::<_, Uuid>(
            "INSERT INTO shadow_intents (id, strategy_id, event_id, dedupe_key, symbol, status, decision, created_at, updated_at, reason) \
             VALUES ($1, $2, $3, $4, $5, $6::shadow_intent_status, $7, $8, $8, $9) \
             ON CONFLICT (dedupe_key) DO NOTHING RETURNING id",
        )
        .bind(intent_id)
        .bind(strategy_id)
        .bind(event_id)
        .bind(dedupe_key)
        .bind(symbol)
        .bind(decision.status.as_db())
        .bind(decision_json)
        .bind(at)
        .bind(&decision.reason)
        .fetch_optional(&self.pool)
        .await
        .context("persist shadow intent")?;
        let Some(intent_id) = inserted else {
            return Ok(false);
        };

        for leg in [decision.spot.as_ref(), decision.perp.as_ref()]
            .into_iter()
            .flatten()
        {
            sqlx::query(
                "INSERT INTO shadow_legs (id, intent_id, venue, side, requested_notional_usd, simulated_fill_notional_usd, simulated_price, fee_usd, impact_bps, status, created_at) \
                 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::shadow_intent_status, $11)",
            )
            .bind(Uuid::new_v4())
            .bind(intent_id)
            .bind(&leg.venue)
            .bind(&leg.side)
            .bind(leg.requested_notional_usd)
            .bind(leg.filled_notional_usd)
            .bind(leg.price)
            .bind(leg.fee_usd)
            .bind(leg.impact_bps)
            .bind(decision.status.as_db())
            .bind(at)
            .execute(&self.pool)
            .await
            .context("persist shadow leg")?;
        }
        Ok(true)
    }

    pub async fn record_feature(
        &self,
        event_id: Uuid,
        symbol: &str,
        observed_at: DateTime<Utc>,
        perp_bid: Option<f64>,
        perp_ask: Option<f64>,
        quote_age_ms: Option<i64>,
    ) -> anyhow::Result<()> {
        sqlx::query(
            "INSERT INTO market_features (event_id, symbol, observed_at, perp_bid, perp_ask, quote_age_ms, source_health) \
             VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (event_id) DO NOTHING",
        )
        .bind(event_id)
        .bind(symbol)
        .bind(observed_at)
        .bind(perp_bid)
        .bind(perp_ask)
        .bind(quote_age_ms)
        .bind(json!({ "perp": "healthy", "spot": "awaiting_verified_quote_adapter" }))
        .execute(&self.pool)
        .await
        .context("persist market feature")?;
        Ok(())
    }

    pub async fn record_market_stats(
        &self,
        event_id: Uuid,
        symbol: &str,
        feature: MarketStatsFeature,
    ) -> anyhow::Result<()> {
        sqlx::query(
            "INSERT INTO market_features (event_id, symbol, observed_at, perp_mark, perp_index, funding_rate, open_interest, source_health) \\
             VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (event_id) DO NOTHING",
        )
        .bind(event_id)
        .bind(symbol)
        .bind(feature.observed_at)
        .bind(feature.mark)
        .bind(feature.index)
        .bind(feature.funding_rate)
        .bind(feature.open_interest)
        .bind(json!({ "perp": "healthy", "spot": "awaiting_verified_quote_adapter" }))
        .execute(&self.pool)
        .await
        .context("persist Lighter market stats")?;
        Ok(())
    }

    pub fn pool(&self) -> &PgPool {
        &self.pool
    }
}

fn compress_payload(raw: &[u8]) -> anyhow::Result<Vec<u8>> {
    zstd::stream::encode_all(raw, 3).context("compress raw market event")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn compresses_wire_payload_without_loss() {
        let raw = br#"{\"source\":\"lighter\",\"nonce\":7}"#;
        let compressed = compress_payload(raw).unwrap();
        assert_eq!(
            zstd::stream::decode_all(compressed.as_slice()).unwrap(),
            raw
        );
    }
}
