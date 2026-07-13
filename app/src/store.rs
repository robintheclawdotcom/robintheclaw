use chrono::{DateTime, Utc};
use serde::Serialize;
use serde_json::Value;
use sqlx::postgres::PgPoolOptions;
use sqlx::{PgPool, Row};
use std::time::Duration;

const REQUIRED_TABLES_SQL: &str = r#"
SELECT to_regclass('public.raw_market_events') IS NOT NULL
   AND to_regclass('public.source_health') IS NOT NULL
   AND to_regclass('public.shadow_intents') IS NOT NULL
   AND to_regclass('public.strategy_candidates') IS NOT NULL
   AND to_regclass('public.dataset_snapshots') IS NOT NULL
"#;

#[derive(Clone)]
pub struct Store {
    pool: PgPool,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SourceHealth {
    pub source: String,
    pub status: String,
    pub last_event_at: Option<DateTime<Utc>>,
    pub last_error: Option<String>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CaptureSummary {
    pub source: String,
    pub kind: String,
    pub event_count: i64,
    pub last_received_at: DateTime<Utc>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ShadowIntent {
    pub id: String,
    pub strategy_version: String,
    pub symbol: String,
    pub status: String,
    pub decision: Value,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
    pub expires_at: Option<DateTime<Utc>>,
    pub reason: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DatasetSnapshot {
    pub id: String,
    pub manifest_sha256: String,
    pub starts_at: DateTime<Utc>,
    pub ends_at: DateTime<Utc>,
    pub source_filter: Value,
    pub created_at: DateTime<Utc>,
}

impl Store {
    pub async fn connect(
        url: &str,
        max_connections: u32,
        timeout_ms: u64,
    ) -> Result<Self, sqlx::Error> {
        let statement_timeout = timeout_ms;
        let pool = PgPoolOptions::new()
            .max_connections(max_connections)
            .acquire_timeout(Duration::from_secs(5))
            .idle_timeout(Duration::from_secs(300))
            .after_connect(move |connection, _| {
                Box::pin(async move {
                    sqlx::query("SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY")
                        .execute(&mut *connection)
                        .await?;
                    sqlx::query("SELECT set_config('statement_timeout', $1, false)")
                        .bind(statement_timeout.to_string())
                        .execute(&mut *connection)
                        .await?;
                    sqlx::query(
                        "SELECT set_config('application_name', 'robin-control-api', false)",
                    )
                    .execute(&mut *connection)
                    .await?;
                    Ok(())
                })
            })
            .connect(url)
            .await?;
        Ok(Self { pool })
    }

    pub async fn ready(&self) -> Result<bool, sqlx::Error> {
        sqlx::query_scalar(REQUIRED_TABLES_SQL)
            .fetch_one(&self.pool)
            .await
    }

    pub async fn source_health(&self) -> Result<Vec<SourceHealth>, sqlx::Error> {
        let rows = sqlx::query(
            "SELECT source, status::text, last_event_at, last_error, updated_at FROM source_health ORDER BY source",
        )
        .fetch_all(&self.pool)
        .await?;
        rows.into_iter()
            .map(|row| {
                Ok(SourceHealth {
                    source: row.try_get("source")?,
                    status: row.try_get("status")?,
                    last_event_at: row.try_get("last_event_at")?,
                    last_error: row.try_get("last_error")?,
                    updated_at: row.try_get("updated_at")?,
                })
            })
            .collect()
    }

    pub async fn capture_summary(&self, hours: i64) -> Result<Vec<CaptureSummary>, sqlx::Error> {
        let rows = sqlx::query(
            r#"
            SELECT source, kind::text, COUNT(*)::bigint AS event_count,
                   MAX(received_at) AS last_received_at
            FROM raw_market_events
            WHERE received_at >= now() - make_interval(hours => $1::int)
            GROUP BY source, kind
            ORDER BY source, kind
            "#,
        )
        .bind(hours as i32)
        .fetch_all(&self.pool)
        .await?;
        rows.into_iter()
            .map(|row| {
                Ok(CaptureSummary {
                    source: row.try_get("source")?,
                    kind: row.try_get("kind")?,
                    event_count: row.try_get("event_count")?,
                    last_received_at: row.try_get("last_received_at")?,
                })
            })
            .collect()
    }

    pub async fn recent_shadow_intents(
        &self,
        limit: i64,
    ) -> Result<Vec<ShadowIntent>, sqlx::Error> {
        let rows = sqlx::query(
            r#"
            SELECT i.id::text, c.version AS strategy_version, i.symbol, i.status::text,
                   i.decision, i.created_at, i.updated_at, i.expires_at, i.reason
            FROM shadow_intents i
            JOIN strategy_candidates c ON c.id = i.strategy_id
            ORDER BY i.updated_at DESC
            LIMIT $1
            "#,
        )
        .bind(limit)
        .fetch_all(&self.pool)
        .await?;
        rows.into_iter()
            .map(|row| {
                Ok(ShadowIntent {
                    id: row.try_get("id")?,
                    strategy_version: row.try_get("strategy_version")?,
                    symbol: row.try_get("symbol")?,
                    status: row.try_get("status")?,
                    decision: row.try_get("decision")?,
                    created_at: row.try_get("created_at")?,
                    updated_at: row.try_get("updated_at")?,
                    expires_at: row.try_get("expires_at")?,
                    reason: row.try_get("reason")?,
                })
            })
            .collect()
    }

    pub async fn dataset_snapshots(&self, limit: i64) -> Result<Vec<DatasetSnapshot>, sqlx::Error> {
        let rows = sqlx::query(
            r#"
            SELECT id::text, manifest_sha256, starts_at, ends_at, source_filter, created_at
            FROM dataset_snapshots
            ORDER BY created_at DESC
            LIMIT $1
            "#,
        )
        .bind(limit)
        .fetch_all(&self.pool)
        .await?;
        rows.into_iter()
            .map(|row| {
                Ok(DatasetSnapshot {
                    id: row.try_get("id")?,
                    manifest_sha256: row.try_get("manifest_sha256")?,
                    starts_at: row.try_get("starts_at")?,
                    ends_at: row.try_get("ends_at")?,
                    source_filter: row.try_get("source_filter")?,
                    created_at: row.try_get("created_at")?,
                })
            })
            .collect()
    }

    #[cfg(test)]
    pub(crate) fn test() -> Self {
        let pool = PgPoolOptions::new()
            .connect_lazy("postgres://local/test")
            .expect("test database URL");
        Self { pool }
    }
}
