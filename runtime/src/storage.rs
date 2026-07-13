use crate::archive::{ArchiveSegment, DailyManifest, ManifestEntry};
use crate::{CanonicalState, Finality, MarketEventKind, RawMarketEvent, ShadowDecision};
use anyhow::Context;
use bytes::Bytes;
use chrono::{DateTime, Utc};
use object_store::{aws::AmazonS3Builder, path::Path, ObjectStore, ObjectStoreExt};
use serde_json::json;
use sqlx::{postgres::PgPoolOptions, PgPool, Row};
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

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ArchiveReceipt {
    pub object_key: String,
    pub content_sha256: String,
    pub event_count: usize,
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
        sqlx::migrate::Migrator::new(std::path::Path::new("./migrations"))
            .await
            .context("load runtime migrations")?
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
        anyhow::ensure!(
            archive_frame_bound(event.raw.len()) <= crate::archive::MAX_SEGMENT_UNCOMPRESSED_BYTES,
            "wire payload is too large for the archive segment format"
        );
        let mut transaction = self.pool.begin().await.context("start event transaction")?;
        sqlx::query("SELECT ensure_event_staging_partition($1)")
            .bind(event.received_at)
            .execute(&mut *transaction)
            .await
            .context("ensure staging partition")?;
        let inserted = sqlx::query_scalar::<_, Uuid>(
            "INSERT INTO raw_market_events (id, schema_version, source, source_session, source_event_id, connector_version, kind, symbol, source_timestamp_ms, received_at, source_sequence, block_number, block_hash, parent_block_hash, canonical_state, finality, payload_sha256, payload) \
             VALUES ($1, $2, $3, $4, $5, $6, $7::market_event_kind, $8, $9, $10, $11, $12, $13, $14, $15::canonical_state, $16::finality_state, $17, $18) \
             ON CONFLICT (source, source_session, source_event_id) DO NOTHING \
             RETURNING id",
        )
        .bind(event.id)
        .bind(&event.schema_version)
        .bind(&event.source)
        .bind(&event.source_session)
        .bind(&event.source_event_id)
        .bind(&event.connector_version)
        .bind(event.kind.as_db())
        .bind(&event.symbol)
        .bind(event.source_timestamp_ms)
        .bind(event.received_at)
        .bind(&event.source_sequence)
        .bind(event.block_number)
        .bind(&event.block_hash)
        .bind(&event.parent_block_hash)
        .bind(event.canonical_state.as_db())
        .bind(event.finality.as_db())
        .bind(&event.payload_sha256)
        .bind(&event.payload)
        .fetch_optional(&mut *transaction)
        .await
        .context("persist market event")?;
        if inserted.is_some() {
            sqlx::query(
                "INSERT INTO event_staging (event_id, received_at, raw_payload) VALUES ($1, $2, $3)",
            )
            .bind(event.id)
            .bind(event.received_at)
            .bind(&event.raw)
            .execute(&mut *transaction)
            .await
            .context("stage raw market event")?;
        } else {
            let accepted_digest = sqlx::query_scalar::<_, String>(
                "SELECT payload_sha256 FROM raw_market_events \
                 WHERE source = $1 AND source_session = $2 AND source_event_id = $3",
            )
            .bind(&event.source)
            .bind(&event.source_session)
            .bind(&event.source_event_id)
            .fetch_one(&mut *transaction)
            .await
            .context("load duplicate event identity")?;
            anyhow::ensure!(
                accepted_digest == event.payload_sha256,
                "source event identity was reused with different wire bytes"
            );
        }
        transaction.commit().await.context("commit market event")?;
        Ok(inserted.is_some())
    }

    pub async fn archive_pending(&self, limit: i64) -> anyhow::Result<Option<ArchiveReceipt>> {
        anyhow::ensure!(limit > 0, "archive batch limit must be positive");
        let events = self.claim_archive_batch(limit).await?;
        if events.is_empty() {
            return Ok(None);
        }
        let event_ids = events.iter().map(|event| event.id).collect::<Vec<_>>();
        let segment = match ArchiveSegment::build(events) {
            Ok(segment) => segment,
            Err(error) => {
                self.release_archive_lease(&event_ids, Some(&error.to_string()))
                    .await?;
                return Err(error);
            }
        };
        if let Err(error) = self
            .objects
            .put(
                &Path::from(segment.object_key.as_str()),
                Bytes::from(segment.compressed.clone()).into(),
            )
            .await
        {
            self.release_archive_lease(&event_ids, Some(&error.to_string()))
                .await?;
            return Err(error).context("upload archive segment");
        }
        self.acknowledge_archive(&segment).await?;
        let event_count = segment.event_count();
        Ok(Some(ArchiveReceipt {
            object_key: segment.object_key,
            content_sha256: segment.content_sha256,
            event_count,
        }))
    }

    async fn claim_archive_batch(&self, limit: i64) -> anyhow::Result<Vec<RawMarketEvent>> {
        let mut transaction = self.pool.begin().await.context("start archive lease")?;
        let anchor = sqlx::query(
            "SELECT e.source, e.source_session, s.received_at \
             FROM event_staging s JOIN raw_market_events e ON e.id = s.event_id \
             WHERE s.state = 'pending' OR (s.state = 'leased' AND s.leased_until < now()) \
             ORDER BY s.received_at, s.event_id LIMIT 1 FOR UPDATE OF s SKIP LOCKED",
        )
        .fetch_optional(&mut *transaction)
        .await
        .context("find archive batch anchor")?;
        let Some(anchor) = anchor else {
            transaction.commit().await?;
            return Ok(Vec::new());
        };
        let source: String = anchor.try_get("source")?;
        let source_session: String = anchor.try_get("source_session")?;
        let starts_at: DateTime<Utc> = anchor.try_get("received_at")?;
        let rows = sqlx::query(
            "SELECT e.id, e.schema_version, e.source, e.source_session, e.source_event_id, e.connector_version, \
                    e.kind::text AS kind, e.symbol, e.source_timestamp_ms, e.received_at, e.source_sequence, \
                    e.block_number, e.block_hash, e.parent_block_hash, e.canonical_state::text AS canonical_state, \
                    e.finality::text AS finality, e.payload_sha256, e.payload, s.raw_payload \
             FROM event_staging s JOIN raw_market_events e ON e.id = s.event_id \
             WHERE e.source = $1 AND e.source_session = $2 \
               AND s.received_at <= $3 + interval '30 seconds' \
               AND (s.state = 'pending' OR (s.state = 'leased' AND s.leased_until < now())) \
             ORDER BY s.received_at, s.event_id LIMIT $4 FOR UPDATE OF s SKIP LOCKED",
        )
        .bind(&source)
        .bind(&source_session)
        .bind(starts_at)
        .bind(limit)
        .fetch_all(&mut *transaction)
        .await
        .context("claim archive batch")?;

        let mut events = Vec::new();
        let mut bytes = 0_usize;
        for row in rows {
            let raw: Vec<u8> = row.try_get("raw_payload")?;
            let framed_size = archive_frame_bound(raw.len());
            if !events.is_empty()
                && bytes.saturating_add(framed_size)
                    > crate::archive::MAX_SEGMENT_UNCOMPRESSED_BYTES
            {
                break;
            }
            bytes = bytes.saturating_add(framed_size);
            let event = event_from_row(&row, raw)?;
            sqlx::query(
                "UPDATE event_staging SET state = 'leased', leased_until = now() + interval '5 minutes', \
                        attempt_count = attempt_count + 1, last_error = NULL \
                 WHERE event_id = $1 AND received_at = $2",
            )
            .bind(event.id)
            .bind(event.received_at)
            .execute(&mut *transaction)
            .await?;
            events.push(event);
        }
        transaction.commit().await.context("commit archive lease")?;
        Ok(events)
    }

    async fn release_archive_lease(
        &self,
        event_ids: &[Uuid],
        error: Option<&str>,
    ) -> anyhow::Result<()> {
        sqlx::query(
            "UPDATE event_staging SET state = 'pending', leased_until = NULL, last_error = $2 \
             WHERE event_id = ANY($1) AND state = 'leased'",
        )
        .bind(event_ids)
        .bind(error)
        .execute(&self.pool)
        .await
        .context("release archive lease")?;
        Ok(())
    }

    async fn acknowledge_archive(&self, segment: &ArchiveSegment) -> anyhow::Result<()> {
        let mut transaction = self
            .pool
            .begin()
            .await
            .context("start archive acknowledgement")?;
        let segment_id = Uuid::new_v4();
        let stored_id = sqlx::query_scalar::<_, Uuid>(
            "INSERT INTO archive_segments (id, object_key, content_sha256, uncompressed_sha256, source, source_session, starts_at, ends_at, event_count, compressed_bytes, uncompressed_bytes) \
             VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) \
             ON CONFLICT (content_sha256) DO UPDATE SET content_sha256 = EXCLUDED.content_sha256 \
             RETURNING id",
        )
        .bind(segment_id)
        .bind(&segment.object_key)
        .bind(&segment.content_sha256)
        .bind(&segment.uncompressed_sha256)
        .bind(&segment.source)
        .bind(&segment.source_session)
        .bind(segment.starts_at)
        .bind(segment.ends_at)
        .bind(segment.event_count() as i32)
        .bind(segment.compressed.len() as i64)
        .bind(segment.uncompressed_bytes as i64)
        .fetch_one(&mut *transaction)
        .await
        .context("record archive segment")?;

        for (position, event_id) in segment.event_ids.iter().enumerate() {
            sqlx::query(
                "INSERT INTO archive_segment_events (segment_id, event_id, position) VALUES ($1, $2, $3) \
                 ON CONFLICT (event_id) DO NOTHING",
            )
            .bind(stored_id)
            .bind(event_id)
            .bind(position as i32)
            .execute(&mut *transaction)
            .await?;
        }
        sqlx::query("UPDATE raw_market_events SET raw_object_key = $2 WHERE id = ANY($1)")
            .bind(&segment.event_ids)
            .bind(&segment.object_key)
            .execute(&mut *transaction)
            .await?;
        let updated = sqlx::query(
            "UPDATE event_staging SET state = 'archived', leased_until = NULL, archived_at = now(), last_error = NULL \
             WHERE event_id = ANY($1) AND state = 'leased'",
        )
        .bind(&segment.event_ids)
        .execute(&mut *transaction)
        .await?
        .rows_affected();
        anyhow::ensure!(
            updated == segment.event_count() as u64,
            "archive lease changed before acknowledgement"
        );
        transaction
            .commit()
            .await
            .context("commit archive acknowledgement")
    }

    pub async fn persist_manifest(&self, manifest: &DailyManifest) -> anyhow::Result<bool> {
        manifest.verify()?;
        let document = serde_json::to_vec(manifest)?;
        let object_key = manifest.object_key();
        self.objects
            .put(
                &Path::from(object_key.as_str()),
                Bytes::from(document).into(),
            )
            .await
            .context("upload archive manifest")?;
        let inserted = sqlx::query_scalar::<_, Uuid>(
            "INSERT INTO archive_manifests (id, day, object_key, manifest_sha256, event_count, segment_count) \
             VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (manifest_sha256) DO NOTHING RETURNING id",
        )
        .bind(Uuid::new_v4())
        .bind(manifest.day)
        .bind(object_key)
        .bind(&manifest.manifest_sha256)
        .bind(manifest.event_count as i64)
        .bind(manifest.entries.len() as i32)
        .fetch_optional(&self.pool)
        .await
        .context("record archive manifest")?;
        Ok(inserted.is_some())
    }

    pub async fn publish_daily_manifest(
        &self,
        day: chrono::NaiveDate,
    ) -> anyhow::Result<Option<bool>> {
        let starts_at = day
            .and_hms_opt(0, 0, 0)
            .context("invalid archive manifest day")?
            .and_utc();
        let ends_at = starts_at + chrono::Duration::days(1);
        let rows = sqlx::query(
            "SELECT object_key, content_sha256, uncompressed_sha256, source, source_session, \
                    starts_at, ends_at, event_count, compressed_bytes, uncompressed_bytes \
             FROM archive_segments WHERE starts_at >= $1 AND starts_at < $2 ORDER BY object_key",
        )
        .bind(starts_at)
        .bind(ends_at)
        .fetch_all(&self.pool)
        .await
        .context("load daily archive segments")?;
        if rows.is_empty() {
            return Ok(None);
        }
        let entries = rows
            .into_iter()
            .map(|row| {
                Ok(ManifestEntry {
                    object_key: row.try_get("object_key")?,
                    content_sha256: row.try_get("content_sha256")?,
                    uncompressed_sha256: row.try_get("uncompressed_sha256")?,
                    source: row.try_get("source")?,
                    source_session: row.try_get("source_session")?,
                    starts_at: row.try_get("starts_at")?,
                    ends_at: row.try_get("ends_at")?,
                    event_count: usize::try_from(row.try_get::<i32, _>("event_count")?)?,
                    compressed_bytes: usize::try_from(row.try_get::<i64, _>("compressed_bytes")?)?,
                    uncompressed_bytes: usize::try_from(
                        row.try_get::<i64, _>("uncompressed_bytes")?,
                    )?,
                })
            })
            .collect::<anyhow::Result<Vec<_>>>()?;
        let manifest = DailyManifest::build(day, entries)?;
        self.persist_manifest(&manifest).await.map(Some)
    }

    pub async fn purge_archived_staging(&self) -> anyhow::Result<u64> {
        let deleted = sqlx::query(
            "DELETE FROM event_staging WHERE state = 'archived' AND archived_at < now() - interval '7 days'",
        )
        .execute(&self.pool)
        .await
        .context("purge expired archive staging payloads")?
        .rows_affected();
        Ok(deleted)
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

fn event_from_row(row: &sqlx::postgres::PgRow, raw: Vec<u8>) -> anyhow::Result<RawMarketEvent> {
    let payload_sha256: String = row.try_get("payload_sha256")?;
    anyhow::ensure!(
        crate::sha256(&raw) == payload_sha256,
        "staged payload digest does not match the accepted event"
    );
    Ok(RawMarketEvent {
        id: row.try_get("id")?,
        schema_version: row.try_get("schema_version")?,
        source: row.try_get("source")?,
        source_session: row.try_get("source_session")?,
        source_event_id: row.try_get("source_event_id")?,
        connector_version: row.try_get("connector_version")?,
        kind: market_event_kind(row.try_get::<String, _>("kind")?.as_str())?,
        symbol: row.try_get("symbol")?,
        source_timestamp_ms: row.try_get("source_timestamp_ms")?,
        received_at: row.try_get("received_at")?,
        source_sequence: row.try_get("source_sequence")?,
        block_number: row.try_get("block_number")?,
        block_hash: row.try_get("block_hash")?,
        parent_block_hash: row.try_get("parent_block_hash")?,
        canonical_state: canonical_state(row.try_get::<String, _>("canonical_state")?.as_str())?,
        finality: finality(row.try_get::<String, _>("finality")?.as_str())?,
        payload_sha256,
        payload: serde_json::from_slice(&raw).context("decode staged event payload")?,
        raw,
    })
}

fn archive_frame_bound(raw_bytes: usize) -> usize {
    raw_bytes
        .saturating_add(2)
        .saturating_div(3)
        .saturating_mul(4)
        .saturating_add(4_096)
}

fn market_event_kind(value: &str) -> anyhow::Result<MarketEventKind> {
    Ok(match value {
        "order_book" => MarketEventKind::OrderBook,
        "ticker" => MarketEventKind::Ticker,
        "trade" => MarketEventKind::Trade,
        "funding" => MarketEventKind::Funding,
        "open_interest" => MarketEventKind::OpenInterest,
        "market_stats" => MarketEventKind::MarketStats,
        "chain_block" => MarketEventKind::ChainBlock,
        "sequencer" => MarketEventKind::Sequencer,
        "pool_state" => MarketEventKind::PoolState,
        "source_health" => MarketEventKind::SourceHealth,
        _ => anyhow::bail!("unknown market event kind: {value}"),
    })
}

fn canonical_state(value: &str) -> anyhow::Result<CanonicalState> {
    Ok(match value {
        "canonical" => CanonicalState::Canonical,
        "orphaned" => CanonicalState::Orphaned,
        "not_applicable" => CanonicalState::NotApplicable,
        _ => anyhow::bail!("unknown canonical state: {value}"),
    })
}

fn finality(value: &str) -> anyhow::Result<Finality> {
    Ok(match value {
        "pending" => Finality::Pending,
        "confirmed" => Finality::Confirmed,
        "l1_posted" => Finality::L1Posted,
        "finalized" => Finality::Finalized,
        "not_applicable" => Finality::NotApplicable,
        _ => anyhow::bail!("unknown finality state: {value}"),
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn database_enum_parsers_are_strict() {
        assert_eq!(market_event_kind("trade").unwrap(), MarketEventKind::Trade);
        assert_eq!(
            canonical_state("orphaned").unwrap(),
            CanonicalState::Orphaned
        );
        assert_eq!(finality("l1_posted").unwrap(), Finality::L1Posted);
        assert!(market_event_kind("unknown").is_err());
    }

    #[test]
    fn archive_frame_bound_accounts_for_base64_expansion() {
        assert_eq!(archive_frame_bound(3), 4_100);
        assert_eq!(archive_frame_bound(4), 4_104);
    }
}
