use crate::api::error::ApiError;
use crate::product::ReadinessEvidenceInput;
use crate::state::AppState;
use actix_web::{web, HttpRequest, HttpResponse};
use chrono::{DateTime, Utc};
use serde::Deserialize;
use uuid::Uuid;

const READINESS_PATH: &str = "/internal/v1/readiness";

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ReadinessSnapshot {
    execution_account_id: Uuid,
    evidence: Vec<ReadinessEvidence>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ReadinessEvidence {
    check_name: String,
    ready: bool,
    source: String,
    evidence_digest: String,
    observed_at: DateTime<Utc>,
    expires_at: DateTime<Utc>,
}

pub async fn record_readiness(
    req: HttpRequest,
    state: web::Data<AppState>,
    body: web::Bytes,
) -> Result<HttpResponse, ApiError> {
    if !state.readiness_auth.is_enabled() {
        return Err(ApiError::ServiceUnavailable(
            "readiness publisher authentication is disabled".to_string(),
        ));
    }
    if !state.product_store.is_enabled() {
        return Err(ApiError::ServiceUnavailable(
            "application database is not configured".to_string(),
        ));
    }
    let authorized = state
        .readiness_auth
        .authorize("POST", READINESS_PATH, req.headers(), &body, Utc::now())
        .map_err(|_| ApiError::Unauthorized)?;
    let claimed = state
        .product_store
        .claim_internal_nonce(
            "readiness",
            &authorized.caller,
            &authorized.nonce,
            authorized.nonce_expires_at,
        )
        .await
        .map_err(ApiError::internal)?;
    if !claimed {
        return Err(ApiError::Unauthorized);
    }
    let snapshot: ReadinessSnapshot = serde_json::from_slice(&body)
        .map_err(|_| ApiError::BadRequest("invalid readiness snapshot".to_string()))?;
    let evidence: Vec<ReadinessEvidenceInput<'_>> = snapshot
        .evidence
        .iter()
        .map(|item| ReadinessEvidenceInput {
            check_name: &item.check_name,
            ready: item.ready,
            source: &item.source,
            evidence_digest: &item.evidence_digest,
            observed_at: item.observed_at,
            expires_at: item.expires_at,
        })
        .collect();
    let readiness = state
        .product_store
        .record_readiness_snapshot(snapshot.execution_account_id, &evidence)
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    Ok(HttpResponse::Accepted().json(readiness))
}
