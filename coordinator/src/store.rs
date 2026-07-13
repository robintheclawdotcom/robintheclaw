use execution::{ExecutionEvent, ExecutionSaga, ExecutionState, PairIntent, SagaError};
use research::PromotionEvidence;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use sqlx::{postgres::PgPoolOptions, types::Json, PgPool, Postgres, Transaction};
use std::time::Duration;
use thiserror::Error;
use uuid::Uuid;

const MAX_EXIT_SUBMISSION_WINDOW_MS: u64 = 15 * 60 * 1_000;
const MAX_EXIT_RECONCILIATION_WINDOW_MS: u64 = 24 * 60 * 60 * 1_000;

#[derive(Clone)]
pub struct Store {
    pool: PgPool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ActionKind {
    SubmitPerp,
    ReconcilePerp,
    SubmitSpot,
    ReconcileSpot,
    UnwindPerp,
    ReconcileUnwind,
    UnwindSpot,
    ReconcileUnwindSpot,
}

impl ActionKind {
    fn as_str(self) -> &'static str {
        match self {
            Self::SubmitPerp => "submit_perp",
            Self::ReconcilePerp => "reconcile_perp",
            Self::SubmitSpot => "submit_spot",
            Self::ReconcileSpot => "reconcile_spot",
            Self::UnwindPerp => "unwind_perp",
            Self::ReconcileUnwind => "reconcile_unwind",
            Self::UnwindSpot => "unwind_spot",
            Self::ReconcileUnwindSpot => "reconcile_unwind_spot",
        }
    }

    fn parse(value: &str) -> Option<Self> {
        match value {
            "submit_perp" => Some(Self::SubmitPerp),
            "reconcile_perp" => Some(Self::ReconcilePerp),
            "submit_spot" => Some(Self::SubmitSpot),
            "reconcile_spot" => Some(Self::ReconcileSpot),
            "unwind_perp" => Some(Self::UnwindPerp),
            "reconcile_unwind" => Some(Self::ReconcileUnwind),
            "unwind_spot" => Some(Self::UnwindSpot),
            "reconcile_unwind_spot" => Some(Self::ReconcileUnwindSpot),
            _ => None,
        }
    }

    fn venue_event_kinds(self) -> Option<&'static [&'static str]> {
        match self {
            Self::ReconcilePerp => Some(&[
                "perp_accepted",
                "perp_partial",
                "perp_filled",
                "perp_rejected",
            ]),
            Self::ReconcileSpot => Some(&["spot_confirmed", "spot_rejected"]),
            Self::ReconcileUnwind => Some(&[
                "unwind_accepted",
                "unwind_partial",
                "unwind_filled",
                "unwind_rejected",
            ]),
            Self::ReconcileUnwindSpot => Some(&["spot_unwind_confirmed", "spot_unwind_rejected"]),
            _ => None,
        }
    }
}

#[derive(Debug, Clone)]
pub struct ClaimedAction {
    pub id: String,
    pub lease_token: String,
    pub intent: PairIntent,
    pub saga: ExecutionSaga,
    pub kind: ActionKind,
    pub payload: Value,
    pub result: Option<Value>,
    pub attempts: u32,
    pub control_version: i64,
}

struct ClaimedActionRow {
    id: String,
    intent_id: String,
    kind: String,
    payload: Value,
    result: Option<Value>,
    attempts: i32,
    intent: Value,
    saga: Value,
    saga_version: i64,
    control_version: i64,
}

struct RecoveryActionRow {
    id: String,
    kind: String,
    payload: Value,
    result: Option<Value>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ClaimPoison {
    Kind,
    Intent,
    Saga,
    Attempts,
}

impl ClaimPoison {
    fn code(self) -> &'static str {
        match self {
            Self::Kind => "claimed_action_kind_invalid",
            Self::Intent => "claimed_intent_invalid",
            Self::Saga => "claimed_saga_invalid",
            Self::Attempts => "claimed_action_attempts_invalid",
        }
    }
}

#[derive(Debug, Clone)]
pub struct NextAction {
    pub kind: ActionKind,
    pub key: String,
    pub payload: Value,
}

#[derive(Debug, Clone)]
pub struct VenueEvent {
    pub id: i64,
    pub kind: String,
    pub payload: Value,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct NewVenueEvent {
    pub source: String,
    pub source_session: String,
    pub source_event_id: String,
    pub source_sequence: i64,
    pub intent_id: String,
    pub kind: String,
    pub payload: Value,
    pub publisher_at_ms: i64,
    pub received_at_ms: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct NewMarketQuote {
    pub source: String,
    pub source_session: String,
    pub source_event_id: String,
    pub source_sequence: i64,
    pub market_manifest: String,
    pub quote_block_hash: String,
    pub mark_price: u32,
    pub publisher_at_ms: i64,
    pub received_at_ms: i64,
    pub expires_at_ms: i64,
    pub intent_id: Option<String>,
    pub spot_unwind_amount_in: Option<String>,
    pub spot_unwind_expected_amount_out: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ExitRequest {
    pub intent_id: String,
    pub quote_source_session: String,
    pub quote_source_event_id: String,
    pub perp_unwind_price: u32,
    pub minimum_unwind_settlement_out: String,
    pub requested_at_ms: u64,
    pub submission_deadline_ms: u64,
    pub reconciliation_deadline_ms: u64,
    pub reason: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RecoveryRequest {
    pub intent_id: String,
    pub requested_at_ms: u64,
    pub reason: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct ExitAuthority {
    quote_source_session: String,
    quote_source_event_id: String,
    quote_expires_at_ms: u64,
    perp_mark_price: u32,
    perp_unwind_price: u32,
    spot_amount_in: String,
    minimum_unwind_settlement_out: String,
    submission_deadline_ms: u64,
    reconciliation_deadline_ms: u64,
}

struct ExitQuoteRow {
    source_session: String,
    source_event_id: String,
    mark_price: u32,
    spot_amount_in: u128,
    expected_amount_out: u128,
    received_at_ms: u64,
    expires_at_ms: u64,
    max_unwind_price_deviation_bps: u32,
    max_spot_slippage_bps: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PerpObservation {
    pub order_id: String,
    pub transaction_hash: String,
    pub client_order_index: u64,
    pub market_index: u32,
    pub is_ask: bool,
    pub reduce_only: bool,
    pub filled_base: String,
    pub average_price: Option<String>,
}

impl PerpObservation {
    pub fn filled_base(&self) -> Option<u64> {
        parse_u64_string(&self.filled_base)
    }

    pub fn average_price(&self) -> Option<u32> {
        self.average_price
            .as_deref()
            .and_then(parse_u64_string)
            .and_then(|value| u32::try_from(value).ok())
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SpotObservation {
    pub spot_intent_id: String,
    pub tx_hash: String,
    pub block_hash: String,
    pub block_number: u64,
    pub finality: String,
    pub config_version: u64,
    pub amount_in: String,
    pub amount_out: String,
}

impl SpotObservation {
    pub fn amount_in(&self) -> Option<u128> {
        parse_u128_string(&self.amount_in)
    }

    pub fn amount_out(&self) -> Option<u128> {
        parse_u128_string(&self.amount_out)
    }
}

#[derive(Debug, Clone)]
pub struct ObservationOutcome {
    pub transition: Option<ExecutionEvent>,
    pub complete: bool,
    pub result: Value,
    pub next: Option<NextAction>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ActionStop {
    Rejected,
    Ambiguous,
    FailedSafe,
}

impl ActionStop {
    fn as_str(self) -> &'static str {
        match self {
            Self::Rejected => "rejected",
            Self::Ambiguous => "ambiguous",
            Self::FailedSafe => "failed_safe",
        }
    }
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
    #[error("execution action does not exist")]
    MissingAction,
    #[error("execution action is invalid")]
    InvalidAction,
    #[error("execution action lease was lost")]
    LeaseLost,
    #[error("authorization nonce was already used")]
    AuthorizationReplay,
    #[error("venue event identity conflicts with stored evidence")]
    VenueEventConflict,
    #[error("reviewed market configuration or authoritative quote is unavailable")]
    MarketAuthorityUnavailable,
    #[error("intent market evidence does not match authoritative configuration")]
    MarketEvidenceMismatch,
    #[error("Lighter signer configuration differs from the reserved nonce scope")]
    LighterConfigDrift,
    #[error("execution coordinator is not active")]
    CoordinatorHalted,
    #[error("execution identity or active episode already exists")]
    Conflict,
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
        if sqlx::query_scalar::<_, i32>("SELECT 1")
            .fetch_one(&self.pool)
            .await
            .is_err()
        {
            return false;
        }
        for table in [
            "public.execution_intents",
            "public.execution_control",
            "public.execution_identifiers",
            "public.execution_unwind_cursors",
            "public.execution_operator_order_index_seq",
            "public.execution_actions",
            "public.execution_api_nonces",
            "public.execution_venue_events",
            "public.execution_market_configs",
            "public.execution_market_quotes",
            "public.execution_venue_source_sessions",
            "public.execution_venue_event_routes",
            "public.execution_lighter_nonce_reservations",
        ] {
            let exists = sqlx::query_scalar::<_, Option<String>>("SELECT to_regclass($1)::text")
                .bind(table)
                .fetch_one(&self.pool)
                .await
                .is_ok_and(|value| value.is_some());
            if !exists {
                return false;
            }
        }
        true
    }

    pub async fn halt(&self, reason: &str) -> Result<(), StoreError> {
        if reason.is_empty() || reason.len() > 128 {
            return Err(StoreError::InvalidAction);
        }
        let mut transaction = self.pool.begin().await?;
        halt_execution(&mut transaction, reason).await?;
        transaction.commit().await?;
        Ok(())
    }

    pub async fn claim_api_nonce(
        &self,
        scope: &str,
        nonce: &str,
        expires_at_unix: i64,
    ) -> Result<(), StoreError> {
        if !matches!(
            scope,
            "intent" | "exit" | "recovery" | "venue_event" | "market_quote"
        ) || nonce.len() < 32
            || nonce.len() > 128
            || expires_at_unix <= 0
        {
            return Err(StoreError::InvalidAction);
        }
        let mut transaction = self.pool.begin().await?;
        sqlx::query("DELETE FROM execution_api_nonces WHERE expires_at <= now()")
            .execute(&mut *transaction)
            .await?;
        let inserted = sqlx::query(
            r#"
            INSERT INTO execution_api_nonces (scope, nonce, expires_at)
            VALUES ($1, $2, to_timestamp($3))
            ON CONFLICT DO NOTHING
            "#,
        )
        .bind(scope)
        .bind(nonce)
        .bind(expires_at_unix)
        .execute(&mut *transaction)
        .await?;
        if inserted.rows_affected() != 1 {
            return Err(StoreError::AuthorizationReplay);
        }
        transaction.commit().await?;
        Ok(())
    }

    pub async fn record_market_quote(&self, quote: &NewMarketQuote) -> Result<bool, StoreError> {
        let exit_quote = match (
            quote.intent_id.as_deref(),
            quote.spot_unwind_amount_in.as_deref(),
            quote.spot_unwind_expected_amount_out.as_deref(),
        ) {
            (None, None, None) => false,
            (Some(intent_id), Some(amount_in), Some(amount_out)) => {
                if quote.source != "execution-authority"
                    || !valid_hash(intent_id)
                    || parse_u128_string(amount_in).is_none()
                    || parse_u128_string(amount_out).is_none()
                {
                    return Err(StoreError::InvalidAction);
                }
                true
            }
            _ => return Err(StoreError::InvalidAction),
        };
        if (!exit_quote && quote.source != "lighter-auth")
            || quote.source_session.is_empty()
            || quote.source_session.len() > 128
            || quote.source_event_id.is_empty()
            || quote.source_event_id.len() > 256
            || quote.source_sequence < 0
            || !valid_hash(&quote.market_manifest)
            || !valid_hash(&quote.quote_block_hash)
            || quote.mark_price == 0
            || quote.publisher_at_ms <= 0
            || quote.received_at_ms <= 0
            || quote.expires_at_ms <= quote.received_at_ms
        {
            return Err(StoreError::InvalidAction);
        }
        let payload = serde_json::to_vec(quote).map_err(|_| StoreError::InvalidAction)?;
        let payload_sha256 = hex::encode(Sha256::digest(payload));
        let mut transaction = self.pool.begin().await?;
        let inserted = sqlx::query(
            r#"
            INSERT INTO execution_market_quotes
                (source, source_session, source_event_id, source_sequence, market_manifest,
                 quote_block_hash, mark_price, payload_sha256, publisher_at, received_at, expires_at,
                 intent_id, spot_unwind_amount_in, spot_unwind_expected_amount_out)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
                    TIMESTAMPTZ 'epoch' + $9 * interval '1 millisecond',
                    TIMESTAMPTZ 'epoch' + $10 * interval '1 millisecond',
                    TIMESTAMPTZ 'epoch' + $11 * interval '1 millisecond', $12, $13, $14)
            ON CONFLICT DO NOTHING
            "#,
        )
        .bind(&quote.source)
        .bind(&quote.source_session)
        .bind(&quote.source_event_id)
        .bind(quote.source_sequence)
        .bind(&quote.market_manifest)
        .bind(&quote.quote_block_hash)
        .bind(i64::from(quote.mark_price))
        .bind(&payload_sha256)
        .bind(quote.publisher_at_ms)
        .bind(quote.received_at_ms)
        .bind(quote.expires_at_ms)
        .bind(&quote.intent_id)
        .bind(&quote.spot_unwind_amount_in)
        .bind(&quote.spot_unwind_expected_amount_out)
        .execute(&mut *transaction)
        .await?;
        if inserted.rows_affected() == 1 {
            transaction.commit().await?;
            return Ok(true);
        }
        type StoredQuote = (
            String,
            String,
            i64,
            String,
            i64,
            i64,
            i64,
            i64,
            Option<String>,
            Option<String>,
            Option<String>,
        );
        let existing = sqlx::query_as::<_, StoredQuote>(
            r#"
            SELECT market_manifest, quote_block_hash, mark_price, payload_sha256,
                   source_sequence,
                   (EXTRACT(EPOCH FROM publisher_at) * 1000)::bigint,
                   (EXTRACT(EPOCH FROM received_at) * 1000)::bigint,
                   (EXTRACT(EPOCH FROM expires_at) * 1000)::bigint,
                   intent_id, spot_unwind_amount_in, spot_unwind_expected_amount_out
            FROM execution_market_quotes
            WHERE source = $1 AND source_session = $2 AND source_event_id = $3
            FOR SHARE
            "#,
        )
        .bind(&quote.source)
        .bind(&quote.source_session)
        .bind(&quote.source_event_id)
        .fetch_optional(&mut *transaction)
        .await?;
        let identical = existing.as_ref().is_some_and(|existing| {
            existing.0 == quote.market_manifest
                && existing.1 == quote.quote_block_hash
                && existing.2 == i64::from(quote.mark_price)
                && existing.3 == payload_sha256
                && existing.4 == quote.source_sequence
                && existing.5 == quote.publisher_at_ms
                && existing.6 == quote.received_at_ms
                && existing.7 == quote.expires_at_ms
                && existing.8 == quote.intent_id
                && existing.9 == quote.spot_unwind_amount_in
                && existing.10 == quote.spot_unwind_expected_amount_out
        });
        if identical {
            transaction.commit().await?;
            return Ok(false);
        }
        if existing.is_none() {
            let reference = sqlx::query_as::<
                _,
                (
                    i64,
                    i64,
                    i64,
                    Option<String>,
                    Option<String>,
                    Option<String>,
                ),
            >(
                r#"
                SELECT mark_price,
                       (EXTRACT(EPOCH FROM publisher_at) * 1000)::bigint,
                       (EXTRACT(EPOCH FROM expires_at) * 1000)::bigint,
                       intent_id, spot_unwind_amount_in, spot_unwind_expected_amount_out
                FROM execution_market_quotes
                WHERE market_manifest = $1 AND quote_block_hash = $2
                  AND received_at = TIMESTAMPTZ 'epoch' + $3 * interval '1 millisecond'
                FOR SHARE
                "#,
            )
            .bind(&quote.market_manifest)
            .bind(&quote.quote_block_hash)
            .bind(quote.received_at_ms)
            .fetch_one(&mut *transaction)
            .await?;
            if reference.0 == i64::from(quote.mark_price)
                && reference.1 == quote.publisher_at_ms
                && reference.2 == quote.expires_at_ms
                && reference.3 == quote.intent_id
                && reference.4 == quote.spot_unwind_amount_in
                && reference.5 == quote.spot_unwind_expected_amount_out
            {
                transaction.commit().await?;
                return Ok(false);
            }
        }
        halt_execution(&mut transaction, "market_quote_identity_conflict").await?;
        sqlx::query(
            "INSERT INTO execution_incidents (severity, kind, details) VALUES ('critical', 'market_quote_identity_conflict', $1)",
        )
        .bind(Json(serde_json::json!({
            "source": quote.source,
            "source_session": quote.source_session,
            "source_event_id": quote.source_event_id,
        })))
        .execute(&mut *transaction)
        .await?;
        transaction.commit().await?;
        Err(StoreError::VenueEventConflict)
    }

    pub async fn record_venue_event(&self, event: &NewVenueEvent) -> Result<bool, StoreError> {
        let source_matches_kind = match event.source.as_str() {
            "lighter-auth" => event.kind.starts_with("perp_") || event.kind.starts_with("unwind_"),
            "robinhood-chain" => event.kind.starts_with("spot_"),
            _ => false,
        };
        if !source_matches_kind
            || event.source_session.is_empty()
            || event.source_session.len() > 128
            || event.source_event_id.is_empty()
            || event.source_event_id.len() > 256
            || event.source_sequence < 0
            || event.intent_id.is_empty()
            || event.publisher_at_ms <= 0
            || event.received_at_ms <= 0
            || !matches!(
                event.kind.as_str(),
                "perp_accepted"
                    | "perp_partial"
                    | "perp_filled"
                    | "perp_rejected"
                    | "spot_confirmed"
                    | "spot_rejected"
                    | "unwind_accepted"
                    | "unwind_partial"
                    | "unwind_filled"
                    | "unwind_rejected"
                    | "spot_unwind_confirmed"
                    | "spot_unwind_rejected"
            )
            || !valid_venue_payload(&event.kind, &event.payload)
        {
            return Err(StoreError::InvalidAction);
        }
        let payload_bytes =
            serde_json::to_vec(&event.payload).map_err(|_| StoreError::InvalidAction)?;
        let payload_sha256 = hex::encode(Sha256::digest(payload_bytes));
        let mut transaction = self.pool.begin().await?;

        let new_session = sqlx::query(
            r#"
            INSERT INTO execution_venue_source_sessions
                (source, source_session, first_sequence, last_sequence,
                 first_received_at, last_received_at)
            VALUES ($1, $2, $3, $3,
                    TIMESTAMPTZ 'epoch' + $4 * interval '1 millisecond',
                    TIMESTAMPTZ 'epoch' + $4 * interval '1 millisecond')
            ON CONFLICT (source, source_session) DO NOTHING
            "#,
        )
        .bind(&event.source)
        .bind(&event.source_session)
        .bind(event.source_sequence)
        .bind(event.received_at_ms)
        .execute(&mut *transaction)
        .await?
        .rows_affected()
            == 1;
        let last_sequence = if new_session {
            event.source_sequence
        } else {
            sqlx::query_scalar::<_, i64>(
                r#"
                SELECT last_sequence
                FROM execution_venue_source_sessions
                WHERE source = $1 AND source_session = $2
                FOR UPDATE
                "#,
            )
            .bind(&event.source)
            .bind(&event.source_session)
            .fetch_one(&mut *transaction)
            .await?
        };
        let sequence_contiguous = new_session
            || last_sequence
                .checked_add(1)
                .is_some_and(|expected| event.source_sequence == expected);
        let inserted = sqlx::query(
            r#"
            INSERT INTO execution_venue_events
                (source, source_session, source_event_id, source_sequence, intent_id, kind,
                 payload, payload_sha256, publisher_at, received_at)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
                    TIMESTAMPTZ 'epoch' + $9 * interval '1 millisecond',
                    TIMESTAMPTZ 'epoch' + $10 * interval '1 millisecond')
            ON CONFLICT (source, source_session, source_event_id) DO NOTHING
            "#,
        )
        .bind(&event.source)
        .bind(&event.source_session)
        .bind(&event.source_event_id)
        .bind(event.source_sequence)
        .bind(&event.intent_id)
        .bind(&event.kind)
        .bind(Json(&event.payload))
        .bind(&payload_sha256)
        .bind(event.publisher_at_ms)
        .bind(event.received_at_ms)
        .execute(&mut *transaction)
        .await?;
        if inserted.rows_affected() == 0 {
            let existing = sqlx::query_as::<_, (String, String, String, i64, i64, i64)>(
                r#"
                SELECT intent_id, kind, payload_sha256, source_sequence,
                       (EXTRACT(EPOCH FROM publisher_at) * 1000)::bigint,
                       (EXTRACT(EPOCH FROM received_at) * 1000)::bigint
                FROM execution_venue_events
                WHERE source = $1 AND source_session = $2 AND source_event_id = $3
                FOR SHARE
                "#,
            )
            .bind(&event.source)
            .bind(&event.source_session)
            .bind(&event.source_event_id)
            .fetch_one(&mut *transaction)
            .await?;
            let identical = existing.0 == event.intent_id
                && existing.1 == event.kind
                && existing.2 == payload_sha256
                && existing.3 == event.source_sequence
                && existing.4 == event.publisher_at_ms
                && existing.5 == event.received_at_ms;
            if identical {
                advance_venue_session(
                    &mut transaction,
                    &event.source,
                    &event.source_session,
                    last_sequence,
                )
                .await?;
                transaction.commit().await?;
                return Ok(false);
            }
            halt_execution(&mut transaction, "venue_event_identity_conflict").await?;
            sqlx::query(
                "INSERT INTO execution_incidents (intent_id, severity, kind, details) VALUES ($1, 'critical', 'venue_event_identity_conflict', $2)",
            )
            .bind(&existing.0)
            .bind(Json(serde_json::json!({
                "source": event.source,
                "source_session": event.source_session,
                "source_event_id": event.source_event_id,
            })))
            .execute(&mut *transaction)
            .await?;
            transaction.commit().await?;
            return Err(StoreError::VenueEventConflict);
        }

        if sequence_contiguous {
            advance_venue_session(
                &mut transaction,
                &event.source,
                &event.source_session,
                last_sequence,
            )
            .await?;
        } else {
            let event_id = sqlx::query_scalar::<_, i64>(
                r#"
                SELECT id FROM execution_venue_events
                WHERE source = $1 AND source_session = $2 AND source_event_id = $3
                "#,
            )
            .bind(&event.source)
            .bind(&event.source_session)
            .bind(&event.source_event_id)
            .fetch_one(&mut *transaction)
            .await?;
            let reason = if event.source_sequence <= last_sequence {
                "source_sequence_late"
            } else {
                "source_sequence_gap"
            };
            quarantine_venue_event(&mut transaction, event_id, &event.intent_id, reason).await?;
        }
        transaction.commit().await?;
        Ok(true)
    }

    pub async fn create_intent(
        &self,
        intent: &PairIntent,
        now_ms: u64,
    ) -> Result<ExecutionSaga, StoreError> {
        intent
            .validate()
            .map_err(|error| StoreError::InvalidIntent(error.to_string()))?;
        if now_ms < intent.created_at_ms || now_ms > intent.deadline_ms {
            return Err(StoreError::InvalidIntent("intent is not current".into()));
        }
        let mut transaction = self.pool.begin().await?;
        let mode = sqlx::query_scalar::<_, String>(
            "SELECT mode FROM execution_control WHERE singleton FOR SHARE",
        )
        .fetch_one(&mut *transaction)
        .await?;
        if mode != "ACTIVE" {
            return Err(StoreError::CoordinatorHalted);
        }
        verify_market_authority(&mut transaction, intent).await?;
        verify_promotion(&mut transaction, &intent.evidence.strategy_version).await?;
        let mut saga = ExecutionSaga::new(&intent.id)?;
        saga.apply(ExecutionEvent::PrecheckPassed)?;
        sqlx::query(
            r#"
            INSERT INTO execution_intents
                (id, strategy_version, symbol, direction, payload, saga, saga_version)
            VALUES ($1, $2, $3, 'long_spot_short_perp', $4, $5, 1)
            "#,
        )
        .bind(&intent.id)
        .bind(&intent.evidence.strategy_version)
        .bind(&intent.symbol)
        .bind(Json(intent))
        .bind(Json(&saga))
        .execute(&mut *transaction)
        .await
        .map_err(classify_insert_error)?;
        sqlx::query(
            "INSERT INTO execution_events (intent_id, saga_version, event) VALUES ($1, 0, $2)",
        )
        .bind(&intent.id)
        .bind(Json(serde_json::json!({"type": "created"})))
        .execute(&mut *transaction)
        .await?;
        sqlx::query(
            "INSERT INTO execution_events (intent_id, saga_version, event) VALUES ($1, 1, $2)",
        )
        .bind(&intent.id)
        .bind(Json(ExecutionEvent::PrecheckPassed))
        .execute(&mut *transaction)
        .await?;
        for (namespace, value) in [
            ("spot_intent", intent.id.clone()),
            ("spot_intent", intent.spot_unwind_intent_id.clone()),
            (
                "lighter_client_order",
                intent.client_order_index.to_string(),
            ),
        ] {
            sqlx::query(
                "INSERT INTO execution_identifiers (namespace, value, intent_id) VALUES ($1, $2, $3)",
            )
            .bind(namespace)
            .bind(value)
            .bind(&intent.id)
            .execute(&mut *transaction)
            .await
            .map_err(classify_insert_error)?;
        }
        for attempt in 0..intent.max_unwind_attempts {
            let value = intent
                .unwind_client_order_index
                .checked_add(u64::from(attempt))
                .ok_or(StoreError::InvalidAction)?
                .to_string();
            sqlx::query(
                "INSERT INTO execution_identifiers (namespace, value, intent_id) VALUES ('lighter_client_order', $1, $2)",
            )
            .bind(value)
            .bind(&intent.id)
            .execute(&mut *transaction)
            .await
            .map_err(classify_insert_error)?;
        }
        enqueue_action(
            &mut transaction,
            &intent.id,
            &NextAction {
                kind: ActionKind::SubmitPerp,
                key: "entry-perp".into(),
                payload: serde_json::json!({}),
            },
        )
        .await?;
        transaction.commit().await?;
        Ok(saga)
    }

    pub async fn request_exit(
        &self,
        request: &ExitRequest,
        now_ms: u64,
    ) -> Result<ExecutionSaga, StoreError> {
        if !valid_hash(&request.intent_id)
            || request.quote_source_session.is_empty()
            || request.quote_source_session.len() > 128
            || request.quote_source_event_id.is_empty()
            || request.quote_source_event_id.len() > 256
            || request.perp_unwind_price == 0
            || parse_u128_string(&request.minimum_unwind_settlement_out).is_none()
            || !matches!(
                request.reason.as_str(),
                "strategy_exit" | "risk_exit" | "operator_exit"
            )
            || request.requested_at_ms.abs_diff(now_ms) > 30_000
            || request.submission_deadline_ms <= now_ms
            || request.submission_deadline_ms > now_ms.saturating_add(MAX_EXIT_SUBMISSION_WINDOW_MS)
            || request.reconciliation_deadline_ms <= request.submission_deadline_ms
            || request.reconciliation_deadline_ms
                > request
                    .submission_deadline_ms
                    .saturating_add(MAX_EXIT_RECONCILIATION_WINDOW_MS)
        {
            return Err(StoreError::InvalidAction);
        }
        let mut transaction = self.pool.begin().await?;
        let current = load_saga(&mut transaction, &request.intent_id).await?;
        if current.perp_filled_base == 0 {
            return Err(StoreError::InvalidAction);
        }
        let live_exit_action = sqlx::query_scalar::<_, String>(
            r#"
            SELECT id
            FROM execution_actions
            WHERE intent_id = $1
              AND status IN ('pending', 'leased')
            ORDER BY created_at
            LIMIT 1
            FOR UPDATE
            "#,
        )
        .bind(&request.intent_id)
        .fetch_optional(&mut *transaction)
        .await?;
        if live_exit_action.is_some() {
            return Err(StoreError::Conflict);
        }
        let intent = load_intent(&mut transaction, &request.intent_id).await?;
        let quote = load_exit_quote(
            &mut transaction,
            &request.intent_id,
            &intent,
            now_ms,
            current.spot_received_raw,
            Some((
                &request.quote_source_session,
                &request.quote_source_event_id,
            )),
        )
        .await?
        .ok_or(StoreError::MarketAuthorityUnavailable)?;
        let minimum = parse_u128_string(&request.minimum_unwind_settlement_out)
            .ok_or(StoreError::InvalidAction)?;
        let authority = build_exit_authority(
            &current,
            quote,
            now_ms,
            request.submission_deadline_ms,
            request.reconciliation_deadline_ms,
            Some(request.perp_unwind_price),
            Some(minimum),
        )
        .ok_or(StoreError::MarketEvidenceMismatch)?;
        let authority = serde_json::to_value(authority).map_err(|_| StoreError::InvalidAction)?;

        let (saga, kind, phase, payload) = match current.state {
            ExecutionState::PerpFilled => {
                if request.reason != "operator_exit" {
                    return Err(StoreError::InvalidAction);
                }
                let saga = transition_saga(
                    &mut transaction,
                    &request.intent_id,
                    ExecutionEvent::UnwindStarted,
                )
                .await?;
                (
                    saga,
                    ActionKind::UnwindPerp,
                    "perp",
                    serde_json::json!({
                        "filled_base": current.perp_filled_base,
                        "unwound_before": 0,
                        "exit_authority": authority,
                        "exit_reason": request.reason,
                    }),
                )
            }
            ExecutionState::Hedged => {
                transition_saga(
                    &mut transaction,
                    &request.intent_id,
                    ExecutionEvent::ExitStarted,
                )
                .await?;
                let saga = transition_saga(
                    &mut transaction,
                    &request.intent_id,
                    ExecutionEvent::UnwindStarted,
                )
                .await?;
                (
                    saga,
                    ActionKind::UnwindPerp,
                    "perp",
                    serde_json::json!({
                        "filled_base": current.perp_filled_base,
                        "unwound_before": 0,
                        "exit_authority": authority,
                        "exit_reason": request.reason,
                    }),
                )
            }
            ExecutionState::Unwinding | ExecutionState::Unhedged => {
                let saga = if current.state == ExecutionState::Unhedged {
                    transition_saga(
                        &mut transaction,
                        &request.intent_id,
                        ExecutionEvent::UnwindStarted,
                    )
                    .await?
                } else {
                    current.clone()
                };
                if current.perp_unwound_base < current.perp_filled_base {
                    (
                        saga,
                        ActionKind::UnwindPerp,
                        "perp",
                        serde_json::json!({
                            "filled_base": current.perp_filled_base - current.perp_unwound_base,
                            "unwound_before": current.perp_unwound_base,
                            "exit_authority": authority,
                            "exit_reason": request.reason,
                        }),
                    )
                } else {
                    (
                        saga,
                        ActionKind::UnwindSpot,
                        "spot",
                        serde_json::json!({
                            "spot_amount": current.spot_received_raw.to_string(),
                            "exit_authority": authority,
                            "exit_reason": request.reason,
                        }),
                    )
                }
            }
            _ => return Err(StoreError::InvalidAction),
        };
        let quote_key = hex::encode(Sha256::digest(format!(
            "{}:{}",
            request.quote_source_session, request.quote_source_event_id
        )));
        let mut payload = payload;
        if request.reason == "operator_exit"
            && matches!(
                current.state,
                ExecutionState::Unwinding | ExecutionState::Unhedged
            )
            && kind == ActionKind::UnwindPerp
        {
            payload
                .as_object_mut()
                .ok_or(StoreError::InvalidAction)?
                .insert("operator_recovery".into(), serde_json::json!(true));
        }
        enqueue_action(
            &mut transaction,
            &request.intent_id,
            &NextAction {
                kind,
                key: format!("exit-{phase}-{}", &quote_key[..24]),
                payload,
            },
        )
        .await?;
        transaction.commit().await?;
        Ok(saga)
    }

    pub async fn request_recovery(
        &self,
        request: &RecoveryRequest,
        now_ms: u64,
    ) -> Result<ExecutionSaga, StoreError> {
        if !valid_hash(&request.intent_id)
            || !matches!(
                request.reason.as_str(),
                "operator_recovery" | "incident_recovery"
            )
            || request.requested_at_ms.abs_diff(now_ms) > 30_000
        {
            return Err(StoreError::InvalidAction);
        }
        let mut transaction = self.pool.begin().await?;
        let saga = load_saga(&mut transaction, &request.intent_id).await?;
        let live_action = sqlx::query_scalar::<_, String>(
            r#"
            SELECT id
            FROM execution_actions
            WHERE intent_id = $1 AND status IN ('pending', 'leased')
            ORDER BY created_at
            LIMIT 1
            FOR UPDATE
            "#,
        )
        .bind(&request.intent_id)
        .fetch_optional(&mut *transaction)
        .await?;
        if live_action.is_some() {
            return Err(StoreError::Conflict);
        }
        let candidates = sqlx::query_as::<_, (String, String, Value, Option<Value>)>(
            r#"
            SELECT id, kind, payload, result
            FROM execution_actions
            WHERE intent_id = $1
              AND status IN ('ambiguous', 'failed_safe')
            ORDER BY updated_at DESC, created_at DESC, id DESC
            FOR SHARE
            "#,
        )
        .bind(&request.intent_id)
        .fetch_all(&mut *transaction)
        .await?
        .into_iter()
        .map(|row| RecoveryActionRow {
            id: row.0,
            kind: row.1,
            payload: row.2,
            result: row.3,
        })
        .collect::<Vec<_>>();
        let (source_action_id, mut next) = candidates
            .iter()
            .find_map(|candidate| recovery_successor(&saga, candidate))
            .ok_or(StoreError::InvalidAction)?;
        let saga = match (saga.state, next.kind) {
            (ExecutionState::Prechecked, ActionKind::ReconcilePerp) => {
                transition_saga(
                    &mut transaction,
                    &request.intent_id,
                    ExecutionEvent::PerpSubmitted,
                )
                .await?
            }
            (
                ExecutionState::Unhedged,
                ActionKind::ReconcileUnwind
                | ActionKind::UnwindSpot
                | ActionKind::ReconcileUnwindSpot,
            ) => {
                transition_saga(
                    &mut transaction,
                    &request.intent_id,
                    ExecutionEvent::UnwindStarted,
                )
                .await?
            }
            _ => saga,
        };
        let key_material = format!(
            "{}:{}:{}:{}",
            source_action_id,
            next.kind.as_str(),
            request.requested_at_ms,
            request.reason
        );
        let digest = hex::encode(Sha256::digest(key_material));
        next.key = format!("recovery-{}-{}", next.kind.as_str(), &digest[..24]);
        enqueue_action(&mut transaction, &request.intent_id, &next).await?;
        transaction.commit().await?;
        Ok(saga)
    }

    pub async fn bind_exit_authority(
        &self,
        action_id: &str,
        worker: &str,
        lease_token: &str,
        now_ms: u64,
    ) -> Result<bool, StoreError> {
        let mut transaction = self.pool.begin().await?;
        let row = sqlx::query_as::<_, (String, String, Value, Value)>(
            r#"
            SELECT action.intent_id, action.kind, intent.payload, intent.saga
            FROM execution_actions action
            JOIN execution_intents intent ON intent.id = action.intent_id
            WHERE action.id = $1 AND action.status = 'leased'
              AND action.lease_owner = $2 AND action.lease_token = $3
            FOR UPDATE OF action, intent
            "#,
        )
        .bind(action_id)
        .bind(worker)
        .bind(lease_token)
        .fetch_optional(&mut *transaction)
        .await?
        .ok_or(StoreError::LeaseLost)?;
        if row.1 != "unwind_perp" && row.1 != "unwind_spot" {
            return Err(StoreError::InvalidAction);
        }
        let existing_payload = sqlx::query_scalar::<_, Value>(
            "SELECT payload FROM execution_actions WHERE id = $1 FOR UPDATE",
        )
        .bind(action_id)
        .fetch_one(&mut *transaction)
        .await?;
        if existing_payload.get("exit_authority").is_some() {
            transaction.commit().await?;
            return Ok(true);
        }
        let intent: PairIntent =
            serde_json::from_value(row.2).map_err(|_| StoreError::InvalidAction)?;
        let saga: ExecutionSaga =
            serde_json::from_value(row.3).map_err(|_| StoreError::InvalidSaga)?;
        let Some(quote) = load_exit_quote(
            &mut transaction,
            &row.0,
            &intent,
            now_ms,
            saga.spot_received_raw,
            None,
        )
        .await?
        else {
            transaction.commit().await?;
            return Ok(false);
        };
        let submission_deadline = quote.expires_at_ms;
        let reconciliation_deadline = now_ms
            .checked_add(MAX_EXIT_RECONCILIATION_WINDOW_MS)
            .ok_or(StoreError::InvalidAction)?;
        let authority = build_exit_authority(
            &saga,
            quote,
            now_ms,
            submission_deadline,
            reconciliation_deadline,
            None,
            None,
        )
        .ok_or(StoreError::MarketEvidenceMismatch)?;
        let authority = serde_json::to_value(authority).map_err(|_| StoreError::InvalidAction)?;
        sqlx::query(
            "UPDATE execution_actions SET payload = jsonb_set(payload, '{exit_authority}', $2), updated_at = now() WHERE id = $1",
        )
        .bind(action_id)
        .bind(Json(&authority))
        .execute(&mut *transaction)
        .await?;
        append_action_event(
            &mut transaction,
            action_id,
            &row.0,
            "exit_authority_bound",
            authority,
        )
        .await?;
        transaction.commit().await?;
        Ok(true)
    }

    pub async fn claim_action(
        &self,
        worker: &str,
        lease_for: Duration,
    ) -> Result<Option<ClaimedAction>, StoreError> {
        if worker.is_empty()
            || worker.len() > 128
            || lease_for.is_zero()
            || lease_for > Duration::from_secs(60)
        {
            return Err(StoreError::InvalidAction);
        }
        let lease_seconds =
            i64::try_from(lease_for.as_secs()).map_err(|_| StoreError::InvalidAction)?;
        let lease_token = Uuid::new_v4().simple().to_string();
        let mut transaction = self.pool.begin().await?;
        let row = sqlx::query_as::<
            _,
            (
                String,
                String,
                String,
                Value,
                Option<Value>,
                i32,
                Value,
                Value,
                i64,
                i64,
            ),
        >(
            r#"
            WITH candidate AS (
                SELECT id
                FROM execution_actions
                WHERE (
                    (status = 'pending' AND available_at <= now()) OR
                    (status = 'leased' AND lease_expires_at <= now())
                  ) AND (
                    kind <> 'submit_perp' OR
                    result ? 'signed' OR
                    result ? 'submission' OR
                    result ? 'send_authorized' OR
                    EXISTS (
                      SELECT 1 FROM execution_control WHERE singleton AND mode = 'ACTIVE'
                    )
                  )
                ORDER BY available_at, created_at
                FOR UPDATE SKIP LOCKED
                LIMIT 1
            ), claimed AS (
                UPDATE execution_actions action
                SET status = 'leased', lease_owner = $1,
                    lease_expires_at = now() + $2 * interval '1 second',
                    lease_token = $3,
                    attempts = attempts + 1, updated_at = now()
                FROM candidate
                WHERE action.id = candidate.id
                RETURNING action.id, action.intent_id, action.kind, action.payload,
                          action.result, action.attempts
            )
            SELECT claimed.id, claimed.intent_id, claimed.kind, claimed.payload, claimed.result,
                   claimed.attempts, intent.payload, intent.saga, intent.saga_version,
                   control.version
            FROM claimed
            JOIN execution_intents intent ON intent.id = claimed.intent_id
            CROSS JOIN execution_control control
            WHERE control.singleton
            "#,
        )
        .bind(worker)
        .bind(lease_seconds)
        .bind(&lease_token)
        .fetch_optional(&mut *transaction)
        .await?;
        let Some((
            id,
            intent_id,
            kind,
            payload,
            result,
            attempts,
            intent,
            saga,
            saga_version,
            control_version,
        )) = row
        else {
            transaction.commit().await?;
            return Ok(None);
        };
        let row = ClaimedActionRow {
            id,
            intent_id,
            kind,
            payload,
            result,
            attempts,
            intent,
            saga,
            saga_version,
            control_version,
        };
        let action = match decode_claimed_action(&row, lease_token.clone()) {
            Ok(action) => action,
            Err(poison) => {
                fail_safe_locked_action(
                    &mut transaction,
                    &row.id,
                    &row.intent_id,
                    worker,
                    &lease_token,
                    poison.code(),
                    serde_json::json!({
                        "action_kind": row.kind,
                        "stage": "claim",
                    }),
                )
                .await?;
                transaction.commit().await?;
                return Ok(None);
            }
        };
        transaction.commit().await?;
        Ok(Some(action))
    }

    pub async fn fail_safe_action(
        &self,
        action_id: &str,
        worker: &str,
        lease_token: &str,
        error_code: &str,
        details: Value,
    ) -> Result<(), StoreError> {
        if error_code.is_empty() || error_code.len() > 128 {
            return Err(StoreError::InvalidAction);
        }
        let mut transaction = self.pool.begin().await?;
        let intent_id = lock_action(&mut transaction, action_id, worker, lease_token).await?;
        fail_safe_locked_action(
            &mut transaction,
            action_id,
            &intent_id,
            worker,
            lease_token,
            error_code,
            details,
        )
        .await?;
        transaction.commit().await?;
        Ok(())
    }

    pub async fn authorize_entry_send(
        &self,
        action_id: &str,
        worker: &str,
        lease_token: &str,
        claimed_control_version: i64,
    ) -> Result<(), StoreError> {
        let mut transaction = self.pool.begin().await?;
        let eligible = sqlx::query_scalar::<_, bool>(
            r#"
            SELECT kind = 'submit_perp'
            FROM execution_actions
            WHERE id = $1 AND status = 'leased' AND lease_owner = $2
              AND lease_token = $3 AND lease_expires_at > now()
            FOR UPDATE
            "#,
        )
        .bind(action_id)
        .bind(worker)
        .bind(lease_token)
        .fetch_optional(&mut *transaction)
        .await?
        .ok_or(StoreError::LeaseLost)?;
        if !eligible {
            return Err(StoreError::InvalidAction);
        }
        let (mode, control_version) = sqlx::query_as::<_, (String, i64)>(
            "SELECT mode, version FROM execution_control WHERE singleton FOR UPDATE",
        )
        .fetch_one(&mut *transaction)
        .await?;
        if mode != "ACTIVE" || control_version != claimed_control_version {
            transaction.commit().await?;
            return Err(StoreError::CoordinatorHalted);
        }
        let updated = sqlx::query(
            r#"
            UPDATE execution_actions
            SET result = jsonb_set(
                    COALESCE(result, '{}'::jsonb),
                    '{send_authorized}',
                    jsonb_build_object('control_version', $4::bigint, 'authorized_at', now()),
                    true
                ),
                updated_at = now()
            WHERE id = $1 AND kind = 'submit_perp' AND status = 'leased'
              AND lease_owner = $2 AND lease_token = $3 AND lease_expires_at > now()
            "#,
        )
        .bind(action_id)
        .bind(worker)
        .bind(lease_token)
        .bind(control_version)
        .execute(&mut *transaction)
        .await?;
        if updated.rows_affected() != 1 {
            return Err(StoreError::LeaseLost);
        }
        transaction.commit().await?;
        Ok(())
    }

    pub async fn authorize_unwind_send(
        &self,
        action_id: &str,
        worker: &str,
        lease_token: &str,
        now_ms: u64,
    ) -> Result<(), StoreError> {
        let mut transaction = self.pool.begin().await?;
        let (intent_id, payload) = sqlx::query_as::<_, (String, Value)>(
            r#"
            SELECT intent_id, payload
            FROM execution_actions
            WHERE id = $1 AND kind IN ('unwind_perp', 'unwind_spot') AND status = 'leased'
              AND lease_owner = $2 AND lease_token = $3 AND lease_expires_at > now()
            FOR UPDATE
            "#,
        )
        .bind(action_id)
        .bind(worker)
        .bind(lease_token)
        .fetch_optional(&mut *transaction)
        .await?
        .ok_or(StoreError::LeaseLost)?;
        let authority: ExitAuthority = serde_json::from_value(
            payload
                .get("exit_authority")
                .cloned()
                .ok_or(StoreError::InvalidAction)?,
        )
        .map_err(|_| StoreError::InvalidAction)?;
        if now_ms > authority.submission_deadline_ms || now_ms > authority.quote_expires_at_ms {
            return Err(StoreError::MarketAuthorityUnavailable);
        }
        let now = i64::try_from(now_ms).map_err(|_| StoreError::InvalidAction)?;
        let quote = sqlx::query_as::<_, (i64, String, String, i32, i32)>(
            r#"
            SELECT quote.mark_price, quote.spot_unwind_amount_in,
                   quote.spot_unwind_expected_amount_out,
                   config.max_unwind_price_deviation_bps, config.max_spot_slippage_bps
            FROM execution_market_quotes quote
            JOIN execution_market_configs config ON config.manifest_id = quote.market_manifest
            WHERE quote.source = 'execution-authority' AND quote.intent_id = $1
              AND quote.source_session = $2 AND quote.source_event_id = $3
              AND quote.expires_at = TIMESTAMPTZ 'epoch' + $4 * interval '1 millisecond'
              AND quote.expires_at > TIMESTAMPTZ 'epoch' + $5 * interval '1 millisecond'
            FOR SHARE OF quote, config
            "#,
        )
        .bind(&intent_id)
        .bind(&authority.quote_source_session)
        .bind(&authority.quote_source_event_id)
        .bind(i64::try_from(authority.quote_expires_at_ms).map_err(|_| StoreError::InvalidAction)?)
        .bind(now)
        .fetch_optional(&mut *transaction)
        .await?;
        let Some(quote) = quote else {
            return Err(StoreError::MarketAuthorityUnavailable);
        };
        let mark_price = u32::try_from(quote.0).map_err(|_| StoreError::MarketEvidenceMismatch)?;
        let spot_amount = parse_u128_string(&quote.1).ok_or(StoreError::MarketEvidenceMismatch)?;
        let expected_out = parse_u128_string(&quote.2).ok_or(StoreError::MarketEvidenceMismatch)?;
        let minimum_out = parse_u128_string(&authority.minimum_unwind_settlement_out)
            .ok_or(StoreError::MarketEvidenceMismatch)?;
        let max_price_deviation =
            u32::try_from(quote.3).map_err(|_| StoreError::MarketEvidenceMismatch)?;
        let max_spot_slippage =
            u32::try_from(quote.4).map_err(|_| StoreError::MarketEvidenceMismatch)?;
        let price_delta = authority.perp_unwind_price.abs_diff(mark_price);
        let price_bounded = authority.perp_unwind_price >= mark_price
            && u128::from(price_delta) * 10_000
                <= u128::from(mark_price) * u128::from(max_price_deviation);
        let minimum_bounded = minimum_out <= expected_out
            && minimum_out
                .checked_mul(10_000)
                .zip(expected_out.checked_mul(u128::from(10_000 - max_spot_slippage)))
                .is_some_and(|(minimum, bound)| minimum >= bound);
        if authority.perp_mark_price != mark_price
            || authority.spot_amount_in != spot_amount.to_string()
            || !price_bounded
            || !minimum_bounded
        {
            return Err(StoreError::MarketEvidenceMismatch);
        }
        let updated = sqlx::query(
            r#"
            UPDATE execution_actions
            SET result = jsonb_set(
                    COALESCE(result, '{}'::jsonb),
                    '{send_authorized}',
                    jsonb_build_object(
                        'quote_source_session', $4::text,
                        'quote_source_event_id', $5::text,
                        'authorized_at_ms', $6::bigint
                    ),
                    true
                ),
                updated_at = now()
            WHERE id = $1 AND kind IN ('unwind_perp', 'unwind_spot') AND status = 'leased'
              AND lease_owner = $2 AND lease_token = $3 AND lease_expires_at > now()
            "#,
        )
        .bind(action_id)
        .bind(worker)
        .bind(lease_token)
        .bind(&authority.quote_source_session)
        .bind(&authority.quote_source_event_id)
        .bind(now)
        .execute(&mut *transaction)
        .await?;
        if updated.rows_affected() != 1 {
            return Err(StoreError::LeaseLost);
        }
        transaction.commit().await?;
        Ok(())
    }

    pub async fn assign_lighter_nonce(
        &self,
        action_id: &str,
        worker: &str,
        lease_token: &str,
        account_index: i64,
        api_key_index: u8,
        observed_next_nonce: i64,
    ) -> Result<i64, StoreError> {
        if account_index <= 0 || !(2..=254).contains(&api_key_index) || observed_next_nonce < 0 {
            return Err(StoreError::InvalidAction);
        }
        let mut transaction = self.pool.begin().await?;
        let (kind, intent_id) = sqlx::query_as::<_, (String, String)>(
            r#"
            SELECT kind, intent_id FROM execution_actions
            WHERE id = $1 AND status = 'leased' AND lease_owner = $2
              AND lease_token = $3 AND lease_expires_at > now()
            FOR UPDATE
            "#,
        )
        .bind(action_id)
        .bind(worker)
        .bind(lease_token)
        .fetch_optional(&mut *transaction)
        .await?
        .ok_or(StoreError::LeaseLost)?;
        if kind != "submit_perp" && kind != "unwind_perp" {
            return Err(StoreError::InvalidAction);
        }
        if let Some((reserved_account, reserved_api_key, nonce)) =
            sqlx::query_as::<_, (i64, i16, i64)>(
                r#"
                SELECT account_index, api_key_index, nonce
                FROM execution_lighter_nonce_reservations
                WHERE action_id = $1
                FOR SHARE
                "#,
            )
            .bind(action_id)
            .fetch_optional(&mut *transaction)
            .await?
        {
            if reserved_account != account_index || reserved_api_key != i16::from(api_key_index) {
                record_lighter_config_drift(
                    &mut transaction,
                    &intent_id,
                    action_id,
                    reserved_account,
                    reserved_api_key,
                    account_index,
                    api_key_index,
                )
                .await?;
                transaction.commit().await?;
                return Err(StoreError::LighterConfigDrift);
            }
            transaction.commit().await?;
            return Ok(nonce);
        }
        sqlx::query(
            r#"
            INSERT INTO execution_venue_nonces (venue, account_index, api_key_index, next_nonce)
            VALUES ('lighter', $1, $2, $3)
            ON CONFLICT (venue, account_index, api_key_index) DO NOTHING
            "#,
        )
        .bind(account_index)
        .bind(i16::from(api_key_index))
        .bind(observed_next_nonce)
        .execute(&mut *transaction)
        .await?;
        let stored = sqlx::query_scalar::<_, i64>(
            r#"
            SELECT next_nonce FROM execution_venue_nonces
            WHERE venue = 'lighter' AND account_index = $1 AND api_key_index = $2
            FOR UPDATE
            "#,
        )
        .bind(account_index)
        .bind(i16::from(api_key_index))
        .fetch_one(&mut *transaction)
        .await?;
        let nonce = stored.max(observed_next_nonce);
        let next = nonce.checked_add(1).ok_or(StoreError::InvalidAction)?;
        sqlx::query(
            r#"
            UPDATE execution_venue_nonces
            SET next_nonce = $3, version = version + 1, updated_at = now()
            WHERE venue = 'lighter' AND account_index = $1 AND api_key_index = $2
            "#,
        )
        .bind(account_index)
        .bind(i16::from(api_key_index))
        .bind(next)
        .execute(&mut *transaction)
        .await?;
        sqlx::query(
            r#"
            INSERT INTO execution_lighter_nonce_reservations
                (action_id, account_index, api_key_index, nonce)
            VALUES ($1, $2, $3, $4)
            "#,
        )
        .bind(action_id)
        .bind(account_index)
        .bind(i16::from(api_key_index))
        .bind(nonce)
        .execute(&mut *transaction)
        .await?;
        let updated = sqlx::query(
            r#"
            UPDATE execution_actions
            SET payload = payload || jsonb_build_object(
                    'nonce', $3::bigint,
                    'lighter_account_index', $5::bigint,
                    'lighter_api_key_index', $6::smallint
                ),
                updated_at = now()
            WHERE id = $1 AND lease_owner = $2 AND status = 'leased'
              AND lease_token = $4
            "#,
        )
        .bind(action_id)
        .bind(worker)
        .bind(nonce)
        .bind(lease_token)
        .bind(account_index)
        .bind(i16::from(api_key_index))
        .execute(&mut *transaction)
        .await?;
        if updated.rows_affected() != 1 {
            return Err(StoreError::LeaseLost);
        }
        transaction.commit().await?;
        Ok(nonce)
    }

    pub async fn validate_lighter_nonce_binding(
        &self,
        action_id: &str,
        account_index: i64,
        api_key_index: u8,
    ) -> Result<(), StoreError> {
        if account_index <= 0 || !(2..=254).contains(&api_key_index) {
            return Err(StoreError::InvalidAction);
        }
        let mut transaction = self.pool.begin().await?;
        let reservation = sqlx::query_as::<_, (String, i64, i16)>(
            r#"
            SELECT action.intent_id, reservation.account_index, reservation.api_key_index
            FROM execution_lighter_nonce_reservations reservation
            JOIN execution_actions action ON action.id = reservation.action_id
            WHERE reservation.action_id = $1
            FOR SHARE OF reservation
            "#,
        )
        .bind(action_id)
        .fetch_optional(&mut *transaction)
        .await?;
        let Some((intent_id, reserved_account, reserved_api_key)) = reservation else {
            transaction.commit().await?;
            return Ok(());
        };
        if reserved_account == account_index && reserved_api_key == i16::from(api_key_index) {
            transaction.commit().await?;
            return Ok(());
        }
        record_lighter_config_drift(
            &mut transaction,
            &intent_id,
            action_id,
            reserved_account,
            reserved_api_key,
            account_index,
            api_key_index,
        )
        .await?;
        transaction.commit().await?;
        Err(StoreError::LighterConfigDrift)
    }

    pub async fn record_action_result(
        &self,
        action_id: &str,
        worker: &str,
        lease_token: &str,
        field: &str,
        value: Value,
    ) -> Result<(), StoreError> {
        if !matches!(field, "signed" | "request" | "submission") {
            return Err(StoreError::InvalidAction);
        }
        let updated = sqlx::query(
            r#"
            UPDATE execution_actions
            SET result = jsonb_set(COALESCE(result, '{}'::jsonb), $3::text[], $4, true),
                updated_at = now()
            WHERE id = $1 AND status = 'leased' AND lease_owner = $2
              AND lease_token = $5 AND lease_expires_at > now()
            "#,
        )
        .bind(action_id)
        .bind(worker)
        .bind(vec![field])
        .bind(Json(value))
        .bind(lease_token)
        .execute(&self.pool)
        .await?;
        if updated.rows_affected() != 1 {
            return Err(StoreError::LeaseLost);
        }
        Ok(())
    }

    pub async fn complete_action(
        &self,
        action_id: &str,
        worker: &str,
        lease_token: &str,
        transition: Option<ExecutionEvent>,
        result: Value,
        next: Option<NextAction>,
    ) -> Result<ExecutionSaga, StoreError> {
        let mut transaction = self.pool.begin().await?;
        let intent_id = lock_action(&mut transaction, action_id, worker, lease_token).await?;
        let saga = if let Some(event) = transition {
            transition_saga(&mut transaction, &intent_id, event).await?
        } else {
            load_saga(&mut transaction, &intent_id).await?
        };
        let updated = sqlx::query(
            r#"
            UPDATE execution_actions
            SET status = 'succeeded',
                result = jsonb_set(COALESCE(result, '{}'::jsonb), '{completion}', $3, true),
                lease_owner = NULL, lease_token = NULL,
                lease_expires_at = NULL, completed_at = now(), updated_at = now()
            WHERE id = $1 AND lease_owner = $2 AND status = 'leased'
            "#,
        )
        .bind(action_id)
        .bind(worker)
        .bind(Json(result.clone()))
        .execute(&mut *transaction)
        .await?;
        if updated.rows_affected() != 1 {
            return Err(StoreError::LeaseLost);
        }
        append_action_event(&mut transaction, action_id, &intent_id, "succeeded", result).await?;
        if let Some(next) = next.as_ref() {
            enqueue_action(&mut transaction, &intent_id, next).await?;
        }
        transaction.commit().await?;
        Ok(saga)
    }

    pub async fn reschedule_action(
        &self,
        action_id: &str,
        worker: &str,
        lease_token: &str,
        delay: Duration,
        error_code: &str,
    ) -> Result<(), StoreError> {
        if delay.is_zero() || delay > Duration::from_secs(60) || error_code.is_empty() {
            return Err(StoreError::InvalidAction);
        }
        let delay_seconds =
            i64::try_from(delay.as_secs()).map_err(|_| StoreError::InvalidAction)?;
        let mut transaction = self.pool.begin().await?;
        let intent_id = lock_action(&mut transaction, action_id, worker, lease_token).await?;
        let updated = sqlx::query(
            r#"
            UPDATE execution_actions
            SET status = 'pending', available_at = now() + $3 * interval '1 second',
                lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
                error_code = $4, updated_at = now()
            WHERE id = $1 AND lease_owner = $2 AND status = 'leased'
            "#,
        )
        .bind(action_id)
        .bind(worker)
        .bind(delay_seconds)
        .bind(error_code)
        .execute(&mut *transaction)
        .await?;
        if updated.rows_affected() != 1 {
            return Err(StoreError::LeaseLost);
        }
        append_action_event(
            &mut transaction,
            action_id,
            &intent_id,
            "pending",
            serde_json::json!({"error_code": error_code, "delay_seconds": delay_seconds}),
        )
        .await?;
        transaction.commit().await?;
        Ok(())
    }

    #[allow(clippy::too_many_arguments)]
    pub async fn stop_action(
        &self,
        action_id: &str,
        worker: &str,
        lease_token: &str,
        stop: ActionStop,
        error_code: &str,
        transition: Option<ExecutionEvent>,
        details: Value,
    ) -> Result<ExecutionSaga, StoreError> {
        if error_code.is_empty() {
            return Err(StoreError::InvalidAction);
        }
        let mut transaction = self.pool.begin().await?;
        let intent_id = lock_action(&mut transaction, action_id, worker, lease_token).await?;
        let saga = if let Some(event) = transition {
            transition_saga(&mut transaction, &intent_id, event).await?
        } else {
            load_saga(&mut transaction, &intent_id).await?
        };
        let status = stop.as_str();
        let updated = sqlx::query(
            r#"
            UPDATE execution_actions
            SET status = $3,
                result = jsonb_set(COALESCE(result, '{}'::jsonb), '{stop}', $4, true),
                error_code = $5, lease_owner = NULL, lease_token = NULL,
                lease_expires_at = NULL, completed_at = now(), updated_at = now()
            WHERE id = $1 AND lease_owner = $2 AND status = 'leased'
            "#,
        )
        .bind(action_id)
        .bind(worker)
        .bind(status)
        .bind(Json(details.clone()))
        .bind(error_code)
        .execute(&mut *transaction)
        .await?;
        if updated.rows_affected() != 1 {
            return Err(StoreError::LeaseLost);
        }
        append_action_event(
            &mut transaction,
            action_id,
            &intent_id,
            status,
            details.clone(),
        )
        .await?;
        sqlx::query(
            r#"
            INSERT INTO execution_incidents (intent_id, severity, kind, details)
            VALUES ($1, $2, $3, $4)
            "#,
        )
        .bind(&intent_id)
        .bind(if stop == ActionStop::Rejected {
            "warning"
        } else {
            "critical"
        })
        .bind(error_code)
        .bind(Json(details))
        .execute(&mut *transaction)
        .await?;
        if stop != ActionStop::Rejected {
            halt_execution(&mut transaction, error_code).await?;
        }
        transaction.commit().await?;
        Ok(saga)
    }

    #[allow(clippy::too_many_arguments)]
    pub async fn continue_ambiguous_action(
        &self,
        action_id: &str,
        worker: &str,
        lease_token: &str,
        error_code: &str,
        transition: Option<ExecutionEvent>,
        details: Value,
        next: NextAction,
    ) -> Result<ExecutionSaga, StoreError> {
        if error_code.is_empty() {
            return Err(StoreError::InvalidAction);
        }
        let mut transaction = self.pool.begin().await?;
        let intent_id = lock_action(&mut transaction, action_id, worker, lease_token).await?;
        let saga = if let Some(event) = transition {
            transition_saga(&mut transaction, &intent_id, event).await?
        } else {
            load_saga(&mut transaction, &intent_id).await?
        };
        let updated = sqlx::query(
            r#"
            UPDATE execution_actions
            SET status = 'ambiguous',
                result = jsonb_set(COALESCE(result, '{}'::jsonb), '{stop}', $4, true),
                error_code = $5, lease_owner = NULL, lease_token = NULL,
                lease_expires_at = NULL, completed_at = now(), updated_at = now()
            WHERE id = $1 AND lease_owner = $2 AND lease_token = $3 AND status = 'leased'
            "#,
        )
        .bind(action_id)
        .bind(worker)
        .bind(lease_token)
        .bind(Json(details.clone()))
        .bind(error_code)
        .execute(&mut *transaction)
        .await?;
        if updated.rows_affected() != 1 {
            return Err(StoreError::LeaseLost);
        }
        append_action_event(
            &mut transaction,
            action_id,
            &intent_id,
            "ambiguous",
            details.clone(),
        )
        .await?;
        sqlx::query(
            "INSERT INTO execution_incidents (intent_id, severity, kind, details) VALUES ($1, 'critical', $2, $3)",
        )
        .bind(&intent_id)
        .bind(error_code)
        .bind(Json(details))
        .execute(&mut *transaction)
        .await?;
        halt_execution(&mut transaction, error_code).await?;
        enqueue_action(&mut transaction, &intent_id, &next).await?;
        transaction.commit().await?;
        Ok(saga)
    }

    pub async fn next_venue_event(
        &self,
        action: &ClaimedAction,
    ) -> Result<Option<VenueEvent>, StoreError> {
        let kinds = action
            .kind
            .venue_event_kinds()
            .ok_or(StoreError::InvalidAction)?;
        let routed = sqlx::query_as::<_, (i64, String, Value)>(
            r#"
            SELECT event.id, event.kind, event.payload
            FROM execution_venue_events event
            JOIN execution_venue_event_routes route ON route.venue_event_id = event.id
            LEFT JOIN execution_applied_venue_events applied
              ON applied.venue_event_id = event.id
            WHERE event.intent_id = $1 AND event.kind = ANY($2)
              AND route.action_id = $3 AND route.disposition = 'matched'
              AND applied.venue_event_id IS NULL
            ORDER BY event.id
            LIMIT 1
            "#,
        )
        .bind(&action.intent.id)
        .bind(kinds)
        .bind(&action.id)
        .fetch_optional(&self.pool)
        .await?;
        if let Some((id, kind, payload)) = routed {
            return Ok(Some(VenueEvent { id, kind, payload }));
        }
        let candidates =
            sqlx::query_as::<_, (i64, String, Value, String, String, String, i64, bool)>(
                r#"
            SELECT event.id, event.kind, event.payload, event.source, event.source_session,
                   event.source_event_id, event.source_sequence,
                   route.venue_event_id IS NOT NULL
            FROM execution_venue_events event
            JOIN execution_venue_source_sessions session
              ON session.source = event.source AND session.source_session = event.source_session
            LEFT JOIN execution_venue_event_routes route
              ON route.venue_event_id = event.id
            LEFT JOIN execution_applied_venue_events applied
              ON applied.venue_event_id = event.id
            WHERE event.intent_id = $1
              AND event.kind = ANY($2)
              AND applied.venue_event_id IS NULL
              AND (
                route.venue_event_id IS NULL OR
                (route.disposition = 'quarantined'
                 AND route.reason = 'source_sequence_gap'
                 AND event.source_sequence <= session.last_sequence)
              )
            ORDER BY event.source, session.first_received_at, event.source_session,
                     event.source_sequence, event.id
            FOR UPDATE OF event SKIP LOCKED
            "#,
            )
            .bind(&action.intent.id)
            .bind(kinds)
            .fetch_all(&self.pool)
            .await?;
        for (
            id,
            kind,
            payload,
            source,
            source_session,
            source_event_id,
            source_sequence,
            was_quarantined,
        ) in candidates
        {
            if venue_event_matches(action, &payload)? {
                if was_quarantined {
                    return Ok(Some(VenueEvent { id, kind, payload }));
                }
                let routed = sqlx::query(
                    r#"
                    INSERT INTO execution_venue_event_routes
                        (venue_event_id, action_id, disposition, reason)
                    VALUES ($1, $2, 'matched', 'action_identity_match')
                    ON CONFLICT (venue_event_id) DO NOTHING
                    "#,
                )
                .bind(id)
                .bind(&action.id)
                .execute(&self.pool)
                .await?;
                if routed.rows_affected() == 1 {
                    return Ok(Some(VenueEvent { id, kind, payload }));
                }
                continue;
            }
            let mut transaction = self.pool.begin().await?;
            let quarantined = sqlx::query(
                r#"
                INSERT INTO execution_venue_event_routes
                    (venue_event_id, disposition, reason)
                VALUES ($1, 'quarantined', 'action_identity_mismatch')
                ON CONFLICT (venue_event_id) DO NOTHING
                "#,
            )
            .bind(id)
            .execute(&mut *transaction)
            .await?;
            if quarantined.rows_affected() == 1 {
                sqlx::query(
                    r#"
                    INSERT INTO execution_incidents (intent_id, severity, kind, details)
                    VALUES ($1, 'warning', 'venue_event_quarantined', $2)
                    "#,
                )
                .bind(&action.intent.id)
                .bind(Json(serde_json::json!({
                    "venue_event_id": id,
                    "source": source,
                    "source_session": source_session,
                    "source_event_id": source_event_id,
                    "source_sequence": source_sequence,
                    "reason": "action_identity_mismatch",
                })))
                .execute(&mut *transaction)
                .await?;
            }
            transaction.commit().await?;
        }
        Ok(None)
    }

    pub async fn apply_venue_event(
        &self,
        action_id: &str,
        worker: &str,
        lease_token: &str,
        venue_event_id: i64,
        outcome: ObservationOutcome,
    ) -> Result<ExecutionSaga, StoreError> {
        let mut transaction = self.pool.begin().await?;
        let intent_id = lock_action(&mut transaction, action_id, worker, lease_token).await?;
        let event = sqlx::query_as::<_, (String, Value, String, Option<String>, String, i64, i64)>(
            r#"
            SELECT event.intent_id, event.payload, route.disposition, route.action_id,
                   route.reason, event.source_sequence, session.last_sequence
            FROM execution_venue_events event
            JOIN execution_venue_event_routes route ON route.venue_event_id = event.id
            JOIN execution_venue_source_sessions session
              ON session.source = event.source AND session.source_session = event.source_session
            WHERE event.id = $1
            FOR SHARE OF event, route, session
            "#,
        )
        .bind(venue_event_id)
        .fetch_optional(&mut *transaction)
        .await?
        .ok_or(StoreError::InvalidAction)?;
        if event.0 != intent_id {
            return Err(StoreError::InvalidAction);
        }
        let route_matches = event.2 == "matched" && event.3.as_deref() == Some(action_id);
        let healed_gap =
            event.2 == "quarantined" && event.4 == "source_sequence_gap" && event.5 <= event.6;
        if !route_matches && !healed_gap {
            return Err(StoreError::InvalidAction);
        }
        if healed_gap {
            let (kind, payload, result, intent) =
                sqlx::query_as::<_, (String, Value, Option<Value>, Value)>(
                    r#"
                    SELECT action.kind, action.payload, action.result, intent.payload
                    FROM execution_actions action
                    JOIN execution_intents intent ON intent.id = action.intent_id
                    WHERE action.id = $1
                    FOR SHARE OF intent
                    "#,
                )
                .bind(action_id)
                .fetch_one(&mut *transaction)
                .await?;
            let kind = ActionKind::parse(&kind).ok_or(StoreError::InvalidAction)?;
            let intent: PairIntent =
                serde_json::from_value(intent).map_err(|_| StoreError::InvalidAction)?;
            if !venue_payload_matches(kind, &intent, &payload, result.as_ref(), &event.1)? {
                return Err(StoreError::InvalidAction);
            }
        }
        let applied = sqlx::query(
            "INSERT INTO execution_applied_venue_events (venue_event_id, action_id) VALUES ($1, $2) ON CONFLICT DO NOTHING",
        )
        .bind(venue_event_id)
        .bind(action_id)
        .execute(&mut *transaction)
        .await?;
        if applied.rows_affected() != 1 {
            return Err(StoreError::InvalidAction);
        }
        let saga = if let Some(event) = outcome.transition {
            transition_saga(&mut transaction, &intent_id, event).await?
        } else {
            load_saga(&mut transaction, &intent_id).await?
        };
        let status = if outcome.complete {
            "succeeded"
        } else {
            "pending"
        };
        let updated = sqlx::query(
            r#"
            UPDATE execution_actions
            SET status = $3, result = $4, available_at = now() + interval '1 second',
                lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
                completed_at = CASE WHEN $5 THEN now() ELSE NULL END, updated_at = now()
            WHERE id = $1 AND lease_owner = $2 AND status = 'leased'
            "#,
        )
        .bind(action_id)
        .bind(worker)
        .bind(status)
        .bind(Json(outcome.result.clone()))
        .bind(outcome.complete)
        .execute(&mut *transaction)
        .await?;
        if updated.rows_affected() != 1 {
            return Err(StoreError::LeaseLost);
        }
        append_action_event(
            &mut transaction,
            action_id,
            &intent_id,
            status,
            outcome.result,
        )
        .await?;
        if let Some(next) = outcome.next.as_ref() {
            if !outcome.complete {
                return Err(StoreError::InvalidAction);
            }
            enqueue_action(&mut transaction, &intent_id, next).await?;
        }
        transaction.commit().await?;
        Ok(saga)
    }
}

fn decode_claimed_action(
    row: &ClaimedActionRow,
    lease_token: String,
) -> Result<ClaimedAction, ClaimPoison> {
    let kind = ActionKind::parse(&row.kind).ok_or(ClaimPoison::Kind)?;
    let attempts = u32::try_from(row.attempts).map_err(|_| ClaimPoison::Attempts)?;
    let intent = decode_claimed_intent(&row.intent, &row.intent_id)?;
    let saga = decode_claimed_saga(&row.saga, &row.intent_id, row.saga_version)?;
    Ok(ClaimedAction {
        id: row.id.clone(),
        lease_token,
        intent,
        saga,
        kind,
        payload: row.payload.clone(),
        result: row.result.clone(),
        attempts,
        control_version: row.control_version,
    })
}

fn decode_claimed_intent(value: &Value, intent_id: &str) -> Result<PairIntent, ClaimPoison> {
    let intent: PairIntent =
        serde_json::from_value(value.clone()).map_err(|_| ClaimPoison::Intent)?;
    if intent.id != intent_id || intent.validate().is_err() {
        return Err(ClaimPoison::Intent);
    }
    Ok(intent)
}

fn decode_claimed_saga(
    value: &Value,
    intent_id: &str,
    stored_version: i64,
) -> Result<ExecutionSaga, ClaimPoison> {
    let saga: ExecutionSaga =
        serde_json::from_value(value.clone()).map_err(|_| ClaimPoison::Saga)?;
    if saga.intent_id != intent_id || u64::try_from(stored_version).ok() != Some(saga.version) {
        return Err(ClaimPoison::Saga);
    }
    Ok(saga)
}

#[allow(clippy::too_many_arguments)]
async fn fail_safe_locked_action(
    transaction: &mut Transaction<'_, Postgres>,
    action_id: &str,
    intent_id: &str,
    worker: &str,
    lease_token: &str,
    error_code: &str,
    details: Value,
) -> Result<(), StoreError> {
    let updated = sqlx::query(
        r#"
        UPDATE execution_actions
        SET status = 'failed_safe',
            result = jsonb_set(COALESCE(result, '{}'::jsonb), '{stop}', $5, true),
            error_code = $4, lease_owner = NULL, lease_token = NULL,
            lease_expires_at = NULL, completed_at = now(), updated_at = now()
        WHERE id = $1 AND intent_id = $2 AND status = 'leased'
          AND lease_owner = $3 AND lease_token = $6
        "#,
    )
    .bind(action_id)
    .bind(intent_id)
    .bind(worker)
    .bind(error_code)
    .bind(Json(details.clone()))
    .bind(lease_token)
    .execute(&mut **transaction)
    .await?;
    if updated.rows_affected() != 1 {
        return Err(StoreError::LeaseLost);
    }
    append_action_event(
        transaction,
        action_id,
        intent_id,
        "failed_safe",
        details.clone(),
    )
    .await?;
    sqlx::query(
        r#"
        INSERT INTO execution_incidents (intent_id, severity, kind, details)
        VALUES ($1, 'critical', $2, $3)
        "#,
    )
    .bind(intent_id)
    .bind(error_code)
    .bind(Json(serde_json::json!({
        "action_id": action_id,
        "context": details,
    })))
    .execute(&mut **transaction)
    .await?;
    halt_execution(transaction, error_code).await?;
    Ok(())
}

async fn quarantine_venue_event(
    transaction: &mut Transaction<'_, Postgres>,
    venue_event_id: i64,
    intent_id: &str,
    reason: &str,
) -> Result<(), StoreError> {
    let inserted = sqlx::query(
        r#"
        INSERT INTO execution_venue_event_routes (venue_event_id, disposition, reason)
        VALUES ($1, 'quarantined', $2)
        ON CONFLICT (venue_event_id) DO NOTHING
        "#,
    )
    .bind(venue_event_id)
    .bind(reason)
    .execute(&mut **transaction)
    .await?;
    if inserted.rows_affected() == 1 {
        sqlx::query(
            r#"
            INSERT INTO execution_incidents (intent_id, severity, kind, details)
            VALUES ($1, 'warning', 'venue_event_quarantined', $2)
            "#,
        )
        .bind(intent_id)
        .bind(Json(serde_json::json!({
            "venue_event_id": venue_event_id,
            "reason": reason,
        })))
        .execute(&mut **transaction)
        .await?;
    }
    Ok(())
}

async fn advance_venue_session(
    transaction: &mut Transaction<'_, Postgres>,
    source: &str,
    source_session: &str,
    last_sequence: i64,
) -> Result<(), StoreError> {
    let frontier = sqlx::query_scalar::<_, i64>(
        r#"
        WITH ordered AS (
            SELECT source_sequence,
                   row_number() OVER (ORDER BY source_sequence)::bigint AS ordinal
            FROM (
                SELECT DISTINCT source_sequence
                FROM execution_venue_events
                WHERE source = $1 AND source_session = $2 AND source_sequence > $3
            ) sequences
        )
        SELECT COALESCE(
            MAX(source_sequence) FILTER (WHERE source_sequence = $3 + ordinal),
            $3
        )
        FROM ordered
        "#,
    )
    .bind(source)
    .bind(source_session)
    .bind(last_sequence)
    .fetch_one(&mut **transaction)
    .await?;
    if frontier == last_sequence {
        return Ok(());
    }
    sqlx::query(
        r#"
        UPDATE execution_venue_source_sessions session
        SET last_sequence = $3,
            last_received_at = GREATEST(
                session.last_received_at,
                (SELECT MAX(event.received_at)
                 FROM execution_venue_events event
                 WHERE event.source = $1 AND event.source_session = $2
                   AND event.source_sequence <= $3)
            )
        WHERE session.source = $1 AND session.source_session = $2
        "#,
    )
    .bind(source)
    .bind(source_session)
    .bind(frontier)
    .execute(&mut **transaction)
    .await?;
    Ok(())
}

async fn record_lighter_config_drift(
    transaction: &mut Transaction<'_, Postgres>,
    intent_id: &str,
    action_id: &str,
    reserved_account_index: i64,
    reserved_api_key_index: i16,
    configured_account_index: i64,
    configured_api_key_index: u8,
) -> Result<(), StoreError> {
    halt_execution(transaction, "lighter_nonce_scope_drift").await?;
    sqlx::query(
        r#"
        INSERT INTO execution_incidents (intent_id, severity, kind, details)
        SELECT $1, 'critical', 'lighter_nonce_scope_drift', $2
        WHERE NOT EXISTS (
            SELECT 1 FROM execution_incidents
            WHERE intent_id = $1 AND kind = 'lighter_nonce_scope_drift'
              AND details ->> 'action_id' = $3 AND resolved_at IS NULL
        )
        "#,
    )
    .bind(intent_id)
    .bind(Json(serde_json::json!({
        "action_id": action_id,
        "reserved_account_index": reserved_account_index,
        "reserved_api_key_index": reserved_api_key_index,
        "configured_account_index": configured_account_index,
        "configured_api_key_index": configured_api_key_index,
    })))
    .bind(action_id)
    .execute(&mut **transaction)
    .await?;
    Ok(())
}

fn venue_event_matches(action: &ClaimedAction, payload: &Value) -> Result<bool, StoreError> {
    venue_payload_matches(
        action.kind,
        &action.intent,
        &action.payload,
        action.result.as_ref(),
        payload,
    )
}

fn venue_payload_matches(
    kind: ActionKind,
    intent: &PairIntent,
    action_payload: &Value,
    action_result: Option<&Value>,
    payload: &Value,
) -> Result<bool, StoreError> {
    match kind {
        ActionKind::ReconcilePerp | ActionKind::ReconcileUnwind => {
            let expected_tx_hash = action_payload
                .get("tx_hash")
                .and_then(Value::as_str)
                .ok_or(StoreError::InvalidAction)?;
            let observation = serde_json::from_value::<PerpObservation>(payload.clone())
                .map_err(|_| StoreError::InvalidAction)?;
            let unwind = kind == ActionKind::ReconcileUnwind;
            let attempt = action_payload
                .get("unwind_attempt")
                .and_then(Value::as_u64)
                .unwrap_or(0);
            let expected_order_index = if unwind {
                action_payload
                    .get("client_order_index")
                    .and_then(Value::as_u64)
                    .map_or_else(
                        || {
                            intent
                                .unwind_client_order_index
                                .checked_add(attempt)
                                .ok_or(StoreError::InvalidAction)
                        },
                        Ok,
                    )?
            } else {
                intent.client_order_index
            };
            let expected_order_id = action_result
                .and_then(|result| result.get("order_id"))
                .and_then(Value::as_str);
            Ok(observation.client_order_index == expected_order_index
                && observation.market_index == intent.lighter_market_index
                && observation.is_ask != unwind
                && observation.reduce_only == unwind
                && observation
                    .transaction_hash
                    .eq_ignore_ascii_case(expected_tx_hash)
                && expected_order_id.is_none_or(|order_id| order_id == observation.order_id))
        }
        ActionKind::ReconcileSpot | ActionKind::ReconcileUnwindSpot => {
            let observation = serde_json::from_value::<SpotObservation>(payload.clone())
                .map_err(|_| StoreError::InvalidAction)?;
            let expected_intent_id = if kind == ActionKind::ReconcileSpot {
                &intent.id
            } else {
                &intent.spot_unwind_intent_id
            };
            Ok(observation.spot_intent_id == *expected_intent_id
                && observation.config_version == intent.spot_config_version
                && valid_hash(&observation.tx_hash))
        }
        _ => Err(StoreError::InvalidAction),
    }
}

async fn lock_action(
    transaction: &mut Transaction<'_, Postgres>,
    action_id: &str,
    worker: &str,
    lease_token: &str,
) -> Result<String, StoreError> {
    sqlx::query_scalar::<_, String>(
        r#"
        SELECT intent_id FROM execution_actions
        WHERE id = $1 AND status = 'leased' AND lease_owner = $2
          AND lease_token = $3 AND lease_expires_at > now()
        FOR UPDATE
        "#,
    )
    .bind(action_id)
    .bind(worker)
    .bind(lease_token)
    .fetch_optional(&mut **transaction)
    .await?
    .ok_or(StoreError::LeaseLost)
}

async fn load_intent(
    transaction: &mut Transaction<'_, Postgres>,
    intent_id: &str,
) -> Result<PairIntent, StoreError> {
    let payload = sqlx::query_scalar::<_, Value>(
        "SELECT payload FROM execution_intents WHERE id = $1 FOR SHARE",
    )
    .bind(intent_id)
    .fetch_optional(&mut **transaction)
    .await?
    .ok_or(StoreError::MissingIntent)?;
    serde_json::from_value(payload).map_err(|_| StoreError::InvalidAction)
}

async fn load_exit_quote(
    transaction: &mut Transaction<'_, Postgres>,
    intent_id: &str,
    intent: &PairIntent,
    now_ms: u64,
    spot_amount_in: u128,
    reference: Option<(&str, &str)>,
) -> Result<Option<ExitQuoteRow>, StoreError> {
    type ExitQuoteDbRow = (String, String, i64, String, String, i64, i64, i32, i32);
    let now = i64::try_from(now_ms).map_err(|_| StoreError::InvalidAction)?;
    let (source_session, source_event_id) = reference.unzip();
    let row = sqlx::query_as::<_, ExitQuoteDbRow>(
        r#"
        SELECT quote.source_session, quote.source_event_id, quote.mark_price,
               quote.spot_unwind_amount_in, quote.spot_unwind_expected_amount_out,
               (EXTRACT(EPOCH FROM quote.received_at) * 1000)::bigint,
               (EXTRACT(EPOCH FROM quote.expires_at) * 1000)::bigint,
               config.max_unwind_price_deviation_bps, config.max_spot_slippage_bps
        FROM execution_market_quotes quote
        JOIN execution_market_configs config ON config.manifest_id = quote.market_manifest
        WHERE quote.source = 'execution-authority'
          AND quote.intent_id = $1
          AND quote.market_manifest = $2
          AND quote.spot_unwind_amount_in IS NOT NULL
          AND quote.spot_unwind_expected_amount_out IS NOT NULL
          AND quote.spot_unwind_amount_in = $6
          AND quote.expires_at > TIMESTAMPTZ 'epoch' + $3 * interval '1 millisecond'
          AND quote.received_at <= TIMESTAMPTZ 'epoch' + $3 * interval '1 millisecond'
          AND config.valid_from <= TIMESTAMPTZ 'epoch' + $3 * interval '1 millisecond'
          AND config.valid_until >= TIMESTAMPTZ 'epoch' + $3 * interval '1 millisecond'
          AND ($4::text IS NULL OR quote.source_session = $4)
          AND ($5::text IS NULL OR quote.source_event_id = $5)
        ORDER BY quote.received_at DESC, quote.id DESC
        LIMIT 1
        FOR SHARE OF quote, config
        "#,
    )
    .bind(intent_id)
    .bind(&intent.evidence.market_manifest)
    .bind(now)
    .bind(source_session)
    .bind(source_event_id)
    .bind(spot_amount_in.to_string())
    .fetch_optional(&mut **transaction)
    .await?;
    row.map(|row| {
        Ok(ExitQuoteRow {
            source_session: row.0,
            source_event_id: row.1,
            mark_price: u32::try_from(row.2).map_err(|_| StoreError::MarketEvidenceMismatch)?,
            spot_amount_in: parse_u128_string(&row.3).ok_or(StoreError::MarketEvidenceMismatch)?,
            expected_amount_out: parse_u128_string(&row.4)
                .ok_or(StoreError::MarketEvidenceMismatch)?,
            received_at_ms: u64::try_from(row.5).map_err(|_| StoreError::MarketEvidenceMismatch)?,
            expires_at_ms: u64::try_from(row.6).map_err(|_| StoreError::MarketEvidenceMismatch)?,
            max_unwind_price_deviation_bps: u32::try_from(row.7)
                .map_err(|_| StoreError::MarketEvidenceMismatch)?,
            max_spot_slippage_bps: u32::try_from(row.8)
                .map_err(|_| StoreError::MarketEvidenceMismatch)?,
        })
    })
    .transpose()
}

fn build_exit_authority(
    saga: &ExecutionSaga,
    quote: ExitQuoteRow,
    now_ms: u64,
    submission_deadline_ms: u64,
    reconciliation_deadline_ms: u64,
    requested_perp_price: Option<u32>,
    requested_minimum_out: Option<u128>,
) -> Option<ExitAuthority> {
    if quote.received_at_ms > now_ms
        || quote.expires_at_ms <= now_ms
        || submission_deadline_ms <= now_ms
        || submission_deadline_ms > quote.expires_at_ms
        || submission_deadline_ms > now_ms.checked_add(MAX_EXIT_SUBMISSION_WINDOW_MS)?
        || reconciliation_deadline_ms <= submission_deadline_ms
        || reconciliation_deadline_ms
            > submission_deadline_ms.checked_add(MAX_EXIT_RECONCILIATION_WINDOW_MS)?
        || quote.spot_amount_in != saga.spot_received_raw
        || (quote.spot_amount_in == 0) != (quote.expected_amount_out == 0)
    {
        return None;
    }
    let max_price_numerator = u128::from(quote.mark_price).checked_mul(u128::from(
        10_000u32.checked_add(quote.max_unwind_price_deviation_bps)?,
    ))?;
    let max_price = u32::try_from(max_price_numerator.div_ceil(10_000)).ok()?;
    let perp_unwind_price = requested_perp_price.unwrap_or(max_price);
    if perp_unwind_price < quote.mark_price || perp_unwind_price > max_price {
        return None;
    }
    let minimum_bound = quote
        .expected_amount_out
        .checked_mul(u128::from(
            10_000u32.checked_sub(quote.max_spot_slippage_bps)?,
        ))?
        .div_ceil(10_000);
    let minimum_unwind_settlement_out = requested_minimum_out.unwrap_or(minimum_bound);
    if minimum_unwind_settlement_out < minimum_bound
        || minimum_unwind_settlement_out > quote.expected_amount_out
    {
        return None;
    }
    Some(ExitAuthority {
        quote_source_session: quote.source_session,
        quote_source_event_id: quote.source_event_id,
        quote_expires_at_ms: quote.expires_at_ms,
        perp_mark_price: quote.mark_price,
        perp_unwind_price,
        spot_amount_in: quote.spot_amount_in.to_string(),
        minimum_unwind_settlement_out: minimum_unwind_settlement_out.to_string(),
        submission_deadline_ms,
        reconciliation_deadline_ms,
    })
}

async fn load_saga(
    transaction: &mut Transaction<'_, Postgres>,
    intent_id: &str,
) -> Result<ExecutionSaga, StoreError> {
    let (stored, version) = sqlx::query_as::<_, (Value, i64)>(
        "SELECT saga, saga_version FROM execution_intents WHERE id = $1 FOR UPDATE",
    )
    .bind(intent_id)
    .fetch_optional(&mut **transaction)
    .await?
    .ok_or(StoreError::MissingIntent)?;
    let saga: ExecutionSaga =
        serde_json::from_value(stored).map_err(|_| StoreError::InvalidSaga)?;
    if saga.version != u64::try_from(version).map_err(|_| StoreError::InvalidSaga)? {
        return Err(StoreError::InvalidSaga);
    }
    Ok(saga)
}

async fn transition_saga(
    transaction: &mut Transaction<'_, Postgres>,
    intent_id: &str,
    event: ExecutionEvent,
) -> Result<ExecutionSaga, StoreError> {
    let mut saga = load_saga(transaction, intent_id).await?;
    let previous_version = saga.version;
    saga.apply(event.clone())?;
    let active = !saga.state.exposure_resolved();
    let updated = sqlx::query(
        r#"
        UPDATE execution_intents
        SET saga = $2, saga_version = $3, active = $4, updated_at = now()
        WHERE id = $1 AND saga_version = $5
        "#,
    )
    .bind(intent_id)
    .bind(Json(&saga))
    .bind(i64::try_from(saga.version).map_err(|_| StoreError::InvalidSaga)?)
    .bind(active)
    .bind(i64::try_from(previous_version).map_err(|_| StoreError::InvalidSaga)?)
    .execute(&mut **transaction)
    .await?;
    if updated.rows_affected() != 1 {
        return Err(StoreError::InvalidSaga);
    }
    sqlx::query(
        "INSERT INTO execution_events (intent_id, saga_version, event) VALUES ($1, $2, $3)",
    )
    .bind(intent_id)
    .bind(i64::try_from(saga.version).map_err(|_| StoreError::InvalidSaga)?)
    .bind(Json(event))
    .execute(&mut **transaction)
    .await?;
    Ok(saga)
}

fn recovery_successor(
    saga: &ExecutionSaga,
    candidate: &RecoveryActionRow,
) -> Option<(String, NextAction)> {
    let next = match saga.state {
        ExecutionState::Prechecked | ExecutionState::PerpSubmitted => {
            recover_perp_reconciliation(candidate)
        }
        ExecutionState::PerpPartial => recover_perp_reconciliation(candidate),
        ExecutionState::PerpFilled => recover_spot_submission(candidate),
        ExecutionState::SpotSubmitted => recover_spot_reconciliation(candidate),
        ExecutionState::Unwinding | ExecutionState::Unhedged => {
            recover_unwind_reconciliation(candidate)
        }
        _ => None,
    }?;
    Some((candidate.id.clone(), next))
}

fn recover_perp_reconciliation(candidate: &RecoveryActionRow) -> Option<NextAction> {
    let payload = match candidate.kind.as_str() {
        "reconcile_perp" => {
            valid_payload_hash(&candidate.payload, "tx_hash")?;
            candidate.payload.clone()
        }
        "submit_perp" => {
            let result = candidate.result.as_ref()?;
            if result.get("send_authorized").is_none() && result.get("submission").is_none() {
                return None;
            }
            serde_json::json!({"tx_hash": result_tx_hash(result)?})
        }
        _ => return None,
    };
    Some(NextAction {
        kind: ActionKind::ReconcilePerp,
        key: String::new(),
        payload,
    })
}

fn recover_spot_submission(candidate: &RecoveryActionRow) -> Option<NextAction> {
    match candidate.kind.as_str() {
        "reconcile_spot" => recover_spot_reconciliation(candidate),
        "submit_spot" => {
            let result = candidate.result.as_ref()?;
            if let Some(payload) = spot_reconciliation_payload(&candidate.payload, result) {
                return Some(NextAction {
                    kind: ActionKind::ReconcileSpot,
                    key: String::new(),
                    payload,
                });
            }
            let request = persisted_robinhood_request(result)?;
            let mut payload = candidate.payload.clone();
            payload
                .as_object_mut()?
                .insert("recovery_request".into(), request);
            Some(NextAction {
                kind: ActionKind::SubmitSpot,
                key: String::new(),
                payload,
            })
        }
        _ => None,
    }
}

fn recover_spot_reconciliation(candidate: &RecoveryActionRow) -> Option<NextAction> {
    if candidate.kind != "reconcile_spot"
        || candidate
            .payload
            .get("filled_base")
            .and_then(Value::as_u64)
            .filter(|value| *value > 0)
            .is_none()
        || !valid_request_id(candidate.payload.get("request_id")?.as_str()?)
    {
        return None;
    }
    if candidate
        .payload
        .get("tx_hash")
        .and_then(Value::as_str)
        .is_some_and(|value| !valid_hash(value))
    {
        return None;
    }
    Some(NextAction {
        kind: ActionKind::ReconcileSpot,
        key: String::new(),
        payload: candidate.payload.clone(),
    })
}

fn recover_unwind_reconciliation(candidate: &RecoveryActionRow) -> Option<NextAction> {
    match candidate.kind.as_str() {
        "reconcile_unwind" => {
            valid_unwind_perp_payload(&candidate.payload)?;
            Some(NextAction {
                kind: ActionKind::ReconcileUnwind,
                key: String::new(),
                payload: candidate.payload.clone(),
            })
        }
        "unwind_perp" => {
            let result = candidate.result.as_ref()?;
            if result.get("send_authorized").is_none() && result.get("submission").is_none() {
                return None;
            }
            let mut payload = candidate.payload.clone();
            payload
                .as_object_mut()?
                .insert("tx_hash".into(), serde_json::json!(result_tx_hash(result)?));
            valid_unwind_perp_payload(&payload)?;
            Some(NextAction {
                kind: ActionKind::ReconcileUnwind,
                key: String::new(),
                payload,
            })
        }
        "reconcile_unwind_spot" => {
            valid_unwind_spot_payload(&candidate.payload)?;
            Some(NextAction {
                kind: ActionKind::ReconcileUnwindSpot,
                key: String::new(),
                payload: candidate.payload.clone(),
            })
        }
        "unwind_spot" => {
            let result = candidate.result.as_ref()?;
            if let Some(payload) = unwind_spot_reconciliation_payload(&candidate.payload, result) {
                return Some(NextAction {
                    kind: ActionKind::ReconcileUnwindSpot,
                    key: String::new(),
                    payload,
                });
            }
            let request = persisted_robinhood_request(result)?;
            let mut payload = candidate.payload.clone();
            payload
                .as_object_mut()?
                .insert("recovery_request".into(), request);
            Some(NextAction {
                kind: ActionKind::UnwindSpot,
                key: String::new(),
                payload,
            })
        }
        _ => None,
    }
}

fn spot_reconciliation_payload(action_payload: &Value, result: &Value) -> Option<Value> {
    let filled_base = action_payload
        .get("filled_base")?
        .as_u64()
        .filter(|value| *value > 0)?;
    let submission = result
        .get("submission")
        .or_else(|| result.get("completion"))?;
    let request_id = submission.get("request_id")?.as_str()?;
    let tx_hash = submission.get("tx_hash")?.as_str()?;
    if !valid_request_id(request_id) || !valid_hash(tx_hash) {
        return None;
    }
    Some(serde_json::json!({
        "filled_base": filled_base,
        "request_id": request_id,
        "tx_hash": tx_hash,
    }))
}

fn unwind_spot_reconciliation_payload(action_payload: &Value, result: &Value) -> Option<Value> {
    let spot_amount = action_payload.get("spot_amount")?.as_str()?;
    parse_u128_string(spot_amount).filter(|amount| *amount > 0)?;
    let submission = result
        .get("submission")
        .or_else(|| result.get("completion"))?;
    let request_id = submission.get("request_id")?.as_str()?;
    let tx_hash = submission.get("tx_hash")?.as_str()?;
    if !valid_request_id(request_id) || !valid_hash(tx_hash) {
        return None;
    }
    Some(serde_json::json!({
        "spot_amount": spot_amount,
        "request_id": request_id,
        "tx_hash": tx_hash,
        "exit_authority": action_payload.get("exit_authority")?,
    }))
}

fn persisted_robinhood_request(result: &Value) -> Option<Value> {
    let request = result.get("request")?;
    let request_id = request.get("request_id")?.as_str()?;
    let intent = request.get("intent")?.as_object()?;
    if !valid_request_id(request_id) || intent.is_empty() {
        return None;
    }
    Some(request.clone())
}

fn result_tx_hash(result: &Value) -> Option<&str> {
    [
        "/submission/tx_hash",
        "/completion/tx_hash",
        "/signed/tx_hash",
    ]
    .into_iter()
    .find_map(|pointer| result.pointer(pointer).and_then(Value::as_str))
    .filter(|value| valid_hash(value))
}

fn valid_payload_hash(payload: &Value, field: &str) -> Option<()> {
    payload
        .get(field)
        .and_then(Value::as_str)
        .filter(|value| valid_hash(value))
        .map(|_| ())
}

fn valid_unwind_perp_payload(payload: &Value) -> Option<()> {
    payload
        .get("filled_base")
        .and_then(Value::as_u64)
        .filter(|value| *value > 0)?;
    valid_payload_hash(payload, "tx_hash")?;
    let has_index = payload
        .get("client_order_index")
        .and_then(Value::as_u64)
        .is_some()
        || payload
            .get("unwind_attempt")
            .and_then(Value::as_u64)
            .is_some_and(|value| value < 8);
    has_index.then_some(())
}

fn valid_unwind_spot_payload(payload: &Value) -> Option<()> {
    parse_u128_string(payload.get("spot_amount")?.as_str()?).filter(|amount| *amount > 0)?;
    valid_request_id(payload.get("request_id")?.as_str()?).then_some(())?;
    payload
        .get("tx_hash")
        .and_then(Value::as_str)
        .is_none_or(valid_hash)
        .then_some(())
}

fn valid_request_id(value: &str) -> bool {
    !value.is_empty() && value.len() <= 128
}

async fn enqueue_action(
    transaction: &mut Transaction<'_, Postgres>,
    intent_id: &str,
    action: &NextAction,
) -> Result<String, StoreError> {
    if action.key.is_empty() || action.key.len() > 128 {
        return Err(StoreError::InvalidAction);
    }
    let mut payload = action.payload.clone();
    if action.kind == ActionKind::UnwindPerp {
        let operator_recovery = payload
            .get("operator_recovery")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        sqlx::query(
            "INSERT INTO execution_unwind_cursors (intent_id) VALUES ($1) ON CONFLICT DO NOTHING",
        )
        .bind(intent_id)
        .execute(&mut **transaction)
        .await?;
        let attempt = sqlx::query_scalar::<_, i16>(
            r#"
            UPDATE execution_unwind_cursors cursor
            SET next_attempt = next_attempt + 1, updated_at = now()
            WHERE intent_id = $1
              AND next_attempt < (
                  SELECT (payload->>'max_unwind_attempts')::smallint
                  FROM execution_intents
                  WHERE id = $1
              )
            RETURNING (next_attempt - 1)::smallint
            "#,
        )
        .bind(intent_id)
        .fetch_optional(&mut **transaction)
        .await?;
        let client_order_index = if let Some(attempt) = attempt {
            let base = sqlx::query_scalar::<_, i64>(
                "SELECT (payload->>'unwind_client_order_index')::bigint FROM execution_intents WHERE id = $1",
            )
            .bind(intent_id)
            .fetch_one(&mut **transaction)
            .await?;
            let client_order_index = base
                .checked_add(i64::from(attempt))
                .and_then(|value| u64::try_from(value).ok())
                .ok_or(StoreError::InvalidAction)?;
            payload
                .as_object_mut()
                .ok_or(StoreError::InvalidAction)?
                .insert("unwind_attempt".into(), serde_json::json!(attempt));
            client_order_index
        } else if operator_recovery {
            allocate_operator_order_index(transaction, intent_id).await?
        } else {
            return Err(StoreError::InvalidAction);
        };
        payload
            .as_object_mut()
            .ok_or(StoreError::InvalidAction)?
            .insert(
                "client_order_index".into(),
                serde_json::json!(client_order_index),
            );
    }
    let id = Uuid::new_v4().simple().to_string();
    sqlx::query(
        r#"
        INSERT INTO execution_actions (id, intent_id, kind, action_key, payload, status)
        VALUES ($1, $2, $3, $4, $5, 'pending')
        "#,
    )
    .bind(&id)
    .bind(intent_id)
    .bind(action.kind.as_str())
    .bind(&action.key)
    .bind(Json(&payload))
    .execute(&mut **transaction)
    .await
    .map_err(classify_insert_error)?;
    append_action_event(
        transaction,
        &id,
        intent_id,
        "pending",
        serde_json::json!({"kind": action.kind.as_str()}),
    )
    .await?;
    Ok(id)
}

async fn allocate_operator_order_index(
    transaction: &mut Transaction<'_, Postgres>,
    intent_id: &str,
) -> Result<u64, StoreError> {
    for _ in 0..16 {
        let value =
            sqlx::query_scalar::<_, i64>("SELECT nextval('execution_operator_order_index_seq')")
                .fetch_one(&mut **transaction)
                .await?;
        let inserted = sqlx::query(
            r#"
            INSERT INTO execution_identifiers (namespace, value, intent_id)
            VALUES ('lighter_client_order', $1, $2)
            ON CONFLICT DO NOTHING
            "#,
        )
        .bind(value.to_string())
        .bind(intent_id)
        .execute(&mut **transaction)
        .await?;
        if inserted.rows_affected() == 1 {
            return u64::try_from(value).map_err(|_| StoreError::InvalidAction);
        }
    }
    Err(StoreError::Conflict)
}

async fn append_action_event(
    transaction: &mut Transaction<'_, Postgres>,
    action_id: &str,
    intent_id: &str,
    status: &str,
    details: Value,
) -> Result<(), StoreError> {
    sqlx::query(
        r#"
        INSERT INTO execution_action_events (action_id, intent_id, status, details)
        VALUES ($1, $2, $3, $4)
        "#,
    )
    .bind(action_id)
    .bind(intent_id)
    .bind(status)
    .bind(Json(details))
    .execute(&mut **transaction)
    .await?;
    Ok(())
}

async fn halt_execution(
    transaction: &mut Transaction<'_, Postgres>,
    reason: &str,
) -> Result<(), StoreError> {
    sqlx::query(
        r#"
        UPDATE execution_control
        SET mode = 'HALTED', reason = $1, version = version + 1, updated_at = now()
        WHERE singleton
        "#,
    )
    .bind(reason)
    .execute(&mut **transaction)
    .await?;
    Ok(())
}

fn valid_venue_payload(kind: &str, payload: &Value) -> bool {
    match kind {
        "perp_accepted" | "unwind_accepted" => {
            serde_json::from_value::<PerpObservation>(payload.clone())
                .is_ok_and(|event| valid_perp_observation(&event, false, false))
        }
        "perp_rejected" | "unwind_rejected" => {
            serde_json::from_value::<PerpObservation>(payload.clone())
                .is_ok_and(|event| valid_perp_observation(&event, true, false))
        }
        "perp_partial" | "perp_filled" | "unwind_partial" | "unwind_filled" => {
            serde_json::from_value::<PerpObservation>(payload.clone())
                .is_ok_and(|event| valid_perp_observation(&event, true, true))
        }
        "spot_confirmed" | "spot_unwind_confirmed" => {
            serde_json::from_value::<SpotObservation>(payload.clone())
                .is_ok_and(|event| valid_spot_observation(&event, true))
        }
        "spot_rejected" | "spot_unwind_rejected" => {
            serde_json::from_value::<SpotObservation>(payload.clone())
                .is_ok_and(|event| valid_spot_observation(&event, false))
        }
        _ => false,
    }
}

fn valid_perp_observation(
    event: &PerpObservation,
    fill_allowed: bool,
    fill_required: bool,
) -> bool {
    let fill = event.filled_base();
    let average_price = event.average_price();
    let valid_fill = match fill {
        Some(0) => !fill_required && average_price.is_none(),
        Some(_) => fill_allowed && average_price.is_some_and(|price| price > 0),
        None => false,
    };
    !event.order_id.is_empty()
        && event.order_id.len() <= 128
        && event.order_id.bytes().all(|byte| byte.is_ascii_graphic())
        && valid_hash(&event.transaction_hash)
        && valid_fill
}

fn valid_spot_observation(event: &SpotObservation, succeeded: bool) -> bool {
    let amounts = event.amount_in().zip(event.amount_out());
    valid_hash(&event.spot_intent_id)
        && valid_hash(&event.tx_hash)
        && valid_hash(&event.block_hash)
        && event.block_number > 0
        && event.finality == "ethereum_final"
        && event.config_version > 0
        && amounts.is_some_and(|(amount_in, amount_out)| {
            if succeeded {
                amount_in > 0 && amount_out > 0
            } else {
                amount_in == 0 && amount_out == 0
            }
        })
}

fn valid_hash(value: &str) -> bool {
    value.len() == 66
        && value.starts_with("0x")
        && value[2..]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        && value[2..].bytes().any(|byte| byte != b'0')
}

fn parse_u64_string(value: &str) -> Option<u64> {
    parse_unsigned_string(value).and_then(|value| value.parse().ok())
}

fn parse_u128_string(value: &str) -> Option<u128> {
    parse_unsigned_string(value).and_then(|value| value.parse().ok())
}

fn parse_unsigned_string(value: &str) -> Option<&str> {
    (!value.is_empty()
        && !value.starts_with('+')
        && value.trim() == value
        && (value == "0" || !value.starts_with('0')))
    .then_some(value)
}

fn classify_insert_error(error: sqlx::Error) -> StoreError {
    if error
        .as_database_error()
        .and_then(|database| database.code())
        .as_deref()
        == Some("23505")
    {
        return StoreError::Conflict;
    }
    StoreError::Database(error)
}

async fn verify_market_authority(
    transaction: &mut Transaction<'_, Postgres>,
    intent: &PairIntent,
) -> Result<(), StoreError> {
    type MarketAuthorityRow = (
        String,
        String,
        i32,
        i16,
        i16,
        i16,
        i64,
        String,
        i32,
        i32,
        i32,
        i64,
        i64,
    );
    let quote_received_at = i64::try_from(intent.evidence.quote_received_at_ms)
        .map_err(|_| StoreError::MarketEvidenceMismatch)?;
    let created_at =
        i64::try_from(intent.created_at_ms).map_err(|_| StoreError::MarketEvidenceMismatch)?;
    let deadline =
        i64::try_from(intent.deadline_ms).map_err(|_| StoreError::MarketEvidenceMismatch)?;
    let row = sqlx::query_as::<_, MarketAuthorityRow>(
        r#"
        SELECT config.symbol, config.spot_token, config.lighter_market_index,
               config.spot_decimals, config.perp_base_decimals, config.perp_price_decimals,
               config.spot_config_version, config.ui_multiplier_e18,
               config.max_price_deviation_bps, config.max_spot_slippage_bps,
               config.max_unwind_price_deviation_bps, quote.mark_price,
               (EXTRACT(EPOCH FROM quote.expires_at) * 1000)::bigint
        FROM execution_market_configs config
        JOIN execution_market_quotes quote
          ON quote.market_manifest = config.manifest_id
        WHERE config.manifest_id = $1
          AND quote.quote_block_hash = $2
          AND quote.received_at = TIMESTAMPTZ 'epoch' + $3 * interval '1 millisecond'
          AND config.valid_from <= TIMESTAMPTZ 'epoch' + $4 * interval '1 millisecond'
          AND config.valid_until >= TIMESTAMPTZ 'epoch' + $5 * interval '1 millisecond'
          AND quote.expires_at >= TIMESTAMPTZ 'epoch' + $5 * interval '1 millisecond'
        ORDER BY quote.id DESC
        LIMIT 1
        FOR SHARE OF config, quote
        "#,
    )
    .bind(&intent.evidence.market_manifest)
    .bind(&intent.evidence.quote_block_hash)
    .bind(quote_received_at)
    .bind(created_at)
    .bind(deadline)
    .fetch_optional(&mut **transaction)
    .await?
    .ok_or(StoreError::MarketAuthorityUnavailable)?;
    let market_index = u32::try_from(row.2).map_err(|_| StoreError::MarketEvidenceMismatch)?;
    let spot_decimals = u8::try_from(row.3).map_err(|_| StoreError::MarketEvidenceMismatch)?;
    let base_decimals = u8::try_from(row.4).map_err(|_| StoreError::MarketEvidenceMismatch)?;
    let price_decimals = u8::try_from(row.5).map_err(|_| StoreError::MarketEvidenceMismatch)?;
    let config_version = u64::try_from(row.6).map_err(|_| StoreError::MarketEvidenceMismatch)?;
    let mark_price = u32::try_from(row.11).map_err(|_| StoreError::MarketEvidenceMismatch)?;
    let quote_expires_at = u64::try_from(row.12).map_err(|_| StoreError::MarketEvidenceMismatch)?;
    let max_price_deviation_bps =
        u32::try_from(row.8).map_err(|_| StoreError::MarketEvidenceMismatch)?;
    let max_spot_slippage_bps =
        u32::try_from(row.9).map_err(|_| StoreError::MarketEvidenceMismatch)?;
    let max_unwind_price_deviation_bps =
        u32::try_from(row.10).map_err(|_| StoreError::MarketEvidenceMismatch)?;
    let price_delta = intent.perp_limit_price.abs_diff(mark_price);
    let within_price_bound = u128::from(price_delta) * 10_000
        <= u128::from(mark_price) * u128::from(max_price_deviation_bps);
    let unwind_price_delta = intent.perp_unwind_price.abs_diff(mark_price);
    let unwind_within_price_bound = u128::from(unwind_price_delta) * 10_000
        <= u128::from(mark_price) * u128::from(max_unwind_price_deviation_bps);
    let minimum_spot_output_is_bounded = intent
        .minimum_spot_amount_out
        .checked_mul(10_000)
        .zip(
            intent
                .raw_spot_amount
                .checked_mul(u128::from(10_000 - max_spot_slippage_bps)),
        )
        .is_some_and(|(minimum, bound)| minimum >= bound);
    let minimum_unwind_output_is_bounded = intent
        .minimum_unwind_settlement_out
        .checked_mul(10_000)
        .zip(
            intent
                .settlement_amount_in
                .checked_mul(u128::from(10_000 - max_spot_slippage_bps)),
        )
        .is_some_and(|(minimum, bound)| minimum >= bound);
    if row.0 != intent.symbol
        || row.1 != intent.spot_token
        || market_index != intent.lighter_market_index
        || spot_decimals != intent.spot_decimals
        || base_decimals != intent.perp_base_decimals
        || price_decimals != intent.perp_price_decimals
        || config_version != intent.spot_config_version
        || row.7 != intent.evidence.ui_multiplier_e18.to_string()
        || mark_price != intent.evidence.perp_mark_price
        || quote_expires_at != intent.evidence.quote_expires_at_ms
        || !within_price_bound
        || !unwind_within_price_bound
        || !minimum_spot_output_is_bounded
        || !minimum_unwind_output_is_bounded
    {
        return Err(StoreError::MarketEvidenceMismatch);
    }
    Ok(())
}

async fn verify_promotion(
    transaction: &mut Transaction<'_, Postgres>,
    strategy_version: &str,
) -> Result<(), StoreError> {
    sqlx::query("SELECT pg_advisory_xact_lock(hashtext($1))")
        .bind(strategy_version)
        .execute(&mut **transaction)
        .await?;
    let stored = sqlx::query_as::<_, (String, Value, String)>(
        r#"
        SELECT promotion.to_state, evidence.evidence, evidence.evidence_sha256
        FROM execution_promotion_events promotion
        JOIN execution_promotion_evidence evidence
          ON evidence.strategy_version = promotion.strategy_version
         AND evidence.evidence_sha256 = promotion.evidence_sha256
        WHERE promotion.strategy_version = $1
        ORDER BY promotion.id DESC
        LIMIT 1
        FOR SHARE
        "#,
    )
    .bind(strategy_version)
    .fetch_optional(&mut **transaction)
    .await?
    .ok_or(StoreError::MissingEvidence)?;
    if stored.0 != "canary_eligible" {
        return Err(StoreError::Promotion(format!(
            "latest state is {}",
            stored.0
        )));
    }
    let evidence: PromotionEvidence =
        serde_json::from_value(stored.1).map_err(|_| StoreError::MissingEvidence)?;
    if evidence.calculate_hash() != stored.2 {
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn claim_decoding_classifies_invalid_kind_intent_and_attempts() {
        assert_eq!(ActionKind::parse("poisoned"), None);
        assert_eq!(
            decode_claimed_intent(&serde_json::json!({}), "intent-1"),
            Err(ClaimPoison::Intent)
        );
        assert!(u32::try_from(-1_i32).is_err());
        assert_eq!(ClaimPoison::Kind.code(), "claimed_action_kind_invalid");
        assert_eq!(
            ClaimPoison::Attempts.code(),
            "claimed_action_attempts_invalid"
        );
    }

    #[test]
    fn claim_decoding_rejects_corrupt_or_mismatched_saga() {
        assert_eq!(
            decode_claimed_saga(&serde_json::json!({}), "intent-1", 0),
            Err(ClaimPoison::Saga)
        );
        let saga = ExecutionSaga::new("intent-1").unwrap();
        let encoded = serde_json::to_value(saga).unwrap();
        assert!(decode_claimed_saga(&encoded, "intent-1", 0).is_ok());
        assert_eq!(
            decode_claimed_saga(&encoded, "intent-2", 0),
            Err(ClaimPoison::Saga)
        );
        assert_eq!(
            decode_claimed_saga(&encoded, "intent-1", 1),
            Err(ClaimPoison::Saga)
        );
    }
}
