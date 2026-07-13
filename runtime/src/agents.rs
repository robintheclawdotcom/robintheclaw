use anyhow::Context;
use chrono::{DateTime, Utc};
use sqlx::{postgres::PgPoolOptions, PgPool};
use std::{env, time::Duration};
use uuid::Uuid;

#[derive(Debug, Clone, sqlx::FromRow)]
pub struct AgentFanout {
    pub evaluation_id: Uuid,
    pub strategy_version: String,
    pub market_event_id: Uuid,
    pub episode_id: Option<Uuid>,
    pub symbol: String,
    pub status: String,
    pub reason: Option<String>,
    pub net_edge_ppm: Option<i64>,
    pub evaluated_at: DateTime<Utc>,
}

#[derive(Clone)]
pub struct AgentStore {
    pool: PgPool,
}

impl AgentStore {
    pub async fn from_env() -> anyhow::Result<Option<Self>> {
        let Ok(database_url) = env::var("AGENT_DATABASE_URL") else {
            return Ok(None);
        };
        anyhow::ensure!(
            !database_url.trim().is_empty(),
            "AGENT_DATABASE_URL is empty"
        );
        Self::connect(&database_url).await.map(Some)
    }

    pub async fn connect(database_url: &str) -> anyhow::Result<Self> {
        let pool = PgPoolOptions::new()
            .max_connections(4)
            .acquire_timeout(Duration::from_secs(10))
            .connect(database_url)
            .await
            .context("connect to agent database")?;
        Ok(Self { pool })
    }

    pub async fn record_evaluation(&self, fanout: &AgentFanout) -> anyhow::Result<u64> {
        let result = sqlx::query(
            r#"
            INSERT INTO agent_paper_events (
                id, agent_id, evaluation_id, market_event_id, episode_id, symbol,
                status, reason, net_edge_ppm, evaluated_at
            )
            SELECT gen_random_uuid(), id, $2, $3, $4, $5, $6, $7, $8, $9
            FROM agents
            WHERE strategy_version = $1 AND mode = 'paper' AND status = 'running'
            ON CONFLICT (agent_id, evaluation_id) DO NOTHING
            "#,
        )
        .bind(&fanout.strategy_version)
        .bind(fanout.evaluation_id)
        .bind(fanout.market_event_id)
        .bind(fanout.episode_id)
        .bind(&fanout.symbol)
        .bind(&fanout.status)
        .bind(&fanout.reason)
        .bind(fanout.net_edge_ppm)
        .bind(fanout.evaluated_at)
        .execute(&self.pool)
        .await
        .context("fan out paper evaluation to agents")?;
        Ok(result.rows_affected())
    }
}
