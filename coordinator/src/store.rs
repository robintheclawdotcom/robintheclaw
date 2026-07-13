use execution::{ExecutionEvent, ExecutionSaga, PairIntent, SagaError};
use research::PromotionEvidence;
use serde_json::Value;
use sqlx::{postgres::PgPoolOptions, types::Json, PgPool, Postgres, Transaction};
use thiserror::Error;

#[derive(Clone)]
pub struct Store {
    pool: PgPool,
}

#[derive(Debug, Error)]
pub enum StoreError {
    #[error("database operation failed")]
    Database(#[from] sqlx::Error),
    #[error("strategy has no promotion evidence")]
    MissingEvidence,
    #[error("promotion evidence digest does not match")]
    EvidenceDigest,
    #[error("strategy has not passed: {0}")]
    Promotion(String),
    #[error("intent is invalid: {0}")]
    InvalidIntent(String),
    #[error("intent does not exist")]
    MissingIntent,
    #[error("execution transition failed: {0}")]
    Transition(#[from] SagaError),
    #[error("stored saga is invalid")]
    InvalidSaga,
}

impl Store {
    pub async fn connect(database_url: &str) -> Result<Self, sqlx::Error> {
        let pool = PgPoolOptions::new()
            .min_connections(1)
            .max_connections(10)
            .connect(database_url)
            .await?;
        Ok(Self { pool })
    }

    pub fn from_pool(pool: PgPool) -> Self {
        Self { pool }
    }

    pub async fn ready(&self) -> bool {
        sqlx::query_scalar::<_, i32>("SELECT 1")
            .fetch_one(&self.pool)
            .await
            .is_ok()
            && sqlx::query_scalar::<_, Option<String>>(
                "SELECT to_regclass('public.execution_intents')::text",
            )
            .fetch_one(&self.pool)
            .await
            .is_ok_and(|value| value.is_some())
    }

    pub async fn create_intent(&self, intent: &PairIntent) -> Result<ExecutionSaga, StoreError> {
        intent
            .validate()
            .map_err(|error| StoreError::InvalidIntent(error.to_string()))?;
        let mut transaction = self.pool.begin().await?;
        verify_promotion(&mut transaction, &intent.evidence.strategy_version).await?;
        let saga = ExecutionSaga::new(&intent.id)?;
        sqlx::query(
            r#"
            INSERT INTO execution_intents
                (id, strategy_version, symbol, direction, payload, saga, saga_version)
            VALUES ($1, $2, $3, 'long_spot_short_perp', $4, $5, 0)
            "#,
        )
        .bind(&intent.id)
        .bind(&intent.evidence.strategy_version)
        .bind(&intent.symbol)
        .bind(Json(intent))
        .bind(Json(&saga))
        .execute(&mut *transaction)
        .await?;
        sqlx::query(
            "INSERT INTO execution_events (intent_id, saga_version, event) VALUES ($1, 0, $2)",
        )
        .bind(&intent.id)
        .bind(Json(serde_json::json!({"type": "created"})))
        .execute(&mut *transaction)
        .await?;
        transaction.commit().await?;
        Ok(saga)
    }

    pub async fn apply_event(
        &self,
        intent_id: &str,
        event: ExecutionEvent,
    ) -> Result<ExecutionSaga, StoreError> {
        let mut transaction = self.pool.begin().await?;
        let stored = sqlx::query_as::<_, (Value, i64)>(
            "SELECT saga, saga_version FROM execution_intents WHERE id = $1 FOR UPDATE",
        )
        .bind(intent_id)
        .fetch_optional(&mut *transaction)
        .await?
        .ok_or(StoreError::MissingIntent)?;
        let mut saga: ExecutionSaga =
            serde_json::from_value(stored.0).map_err(|_| StoreError::InvalidSaga)?;
        if saga.version != stored.1 as u64 {
            return Err(StoreError::InvalidSaga);
        }
        saga.apply(event.clone())?;
        let active = !saga.state.is_terminal();
        sqlx::query(
            r#"
            UPDATE execution_intents
            SET saga = $2, saga_version = $3, active = $4, updated_at = now()
            WHERE id = $1 AND saga_version = $5
            "#,
        )
        .bind(intent_id)
        .bind(Json(&saga))
        .bind(saga.version as i64)
        .bind(active)
        .bind(stored.1)
        .execute(&mut *transaction)
        .await?;
        sqlx::query(
            "INSERT INTO execution_events (intent_id, saga_version, event) VALUES ($1, $2, $3)",
        )
        .bind(intent_id)
        .bind(saga.version as i64)
        .bind(Json(event))
        .execute(&mut *transaction)
        .await?;
        transaction.commit().await?;
        Ok(saga)
    }

    pub async fn reserve_nonce(
        &self,
        venue: &str,
        account_index: i64,
        api_key_index: i16,
        observed_next_nonce: i64,
    ) -> Result<i64, StoreError> {
        if venue.is_empty()
            || account_index <= 0
            || !(2..=254).contains(&api_key_index)
            || observed_next_nonce < 0
        {
            return Err(StoreError::InvalidIntent("invalid nonce identity".into()));
        }
        let mut transaction = self.pool.begin().await?;
        sqlx::query(
            r#"
            INSERT INTO execution_venue_nonces
                (venue, account_index, api_key_index, next_nonce)
            VALUES ($1, $2, $3, $4)
            ON CONFLICT (venue, account_index, api_key_index) DO NOTHING
            "#,
        )
        .bind(venue)
        .bind(account_index)
        .bind(api_key_index)
        .bind(observed_next_nonce)
        .execute(&mut *transaction)
        .await?;
        let stored = sqlx::query_scalar::<_, i64>(
            r#"
            SELECT next_nonce
            FROM execution_venue_nonces
            WHERE venue = $1 AND account_index = $2 AND api_key_index = $3
            FOR UPDATE
            "#,
        )
        .bind(venue)
        .bind(account_index)
        .bind(api_key_index)
        .fetch_one(&mut *transaction)
        .await?;
        let nonce = stored.max(observed_next_nonce);
        let next = nonce
            .checked_add(1)
            .ok_or_else(|| StoreError::InvalidIntent("nonce exhausted".into()))?;
        sqlx::query(
            r#"
            UPDATE execution_venue_nonces
            SET next_nonce = $4, version = version + 1, updated_at = now()
            WHERE venue = $1 AND account_index = $2 AND api_key_index = $3
            "#,
        )
        .bind(venue)
        .bind(account_index)
        .bind(api_key_index)
        .bind(next)
        .execute(&mut *transaction)
        .await?;
        transaction.commit().await?;
        Ok(nonce)
    }
}

async fn verify_promotion(
    transaction: &mut Transaction<'_, Postgres>,
    strategy_version: &str,
) -> Result<(), StoreError> {
    let stored = sqlx::query_as::<_, (Value, String)>(
        r#"
        SELECT evidence.evidence, evidence.evidence_sha256
        FROM execution_promotion_events promotion
        JOIN execution_promotion_evidence evidence
          ON evidence.strategy_version = promotion.strategy_version
         AND evidence.evidence_sha256 = promotion.evidence_sha256
        WHERE promotion.strategy_version = $1
          AND promotion.to_state = 'canary_eligible'
        ORDER BY promotion.id DESC
        LIMIT 1
        FOR SHARE
        "#,
    )
    .bind(strategy_version)
    .fetch_optional(&mut **transaction)
    .await?
    .ok_or(StoreError::MissingEvidence)?;
    let evidence: PromotionEvidence =
        serde_json::from_value(stored.0).map_err(|_| StoreError::MissingEvidence)?;
    if evidence.calculate_hash() != stored.1 {
        return Err(StoreError::EvidenceDigest);
    }
    let failures = evidence.canary_failures();
    if failures.is_empty() {
        return Ok(());
    }
    Err(StoreError::Promotion(
        serde_json::to_string(&failures).unwrap_or_else(|_| "invalid evidence".into()),
    ))
}
