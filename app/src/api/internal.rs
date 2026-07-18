use crate::api::error::ApiError;
use crate::product::ReadinessEvidenceInput;
use crate::service_auth::{AuthorizedRequest, ServiceAuth};
use crate::state::AppState;
use actix_web::{web, HttpRequest, HttpResponse};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
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
    signed_readiness_response(&state.readiness_auth, &authorized, &readiness)
}

fn signed_readiness_response<T: Serialize>(
    auth: &ServiceAuth,
    request: &AuthorizedRequest,
    value: &T,
) -> Result<HttpResponse, ApiError> {
    let body = serde_json::to_vec(value).map_err(ApiError::internal)?;
    let signature = auth
        .sign_response(
            READINESS_PATH,
            request,
            actix_web::http::StatusCode::ACCEPTED.as_u16(),
            &body,
        )
        .map_err(ApiError::internal)?;
    Ok(HttpResponse::Accepted()
        .insert_header(("X-RTC-Response-Signature", signature))
        .content_type("application/json")
        .body(body))
}

#[cfg(test)]
mod tests {
    use super::*;
    use actix_web::body::to_bytes;
    use hmac::{Hmac, Mac};
    use sha2::{Digest, Sha256};

    #[actix_web::test]
    async fn readiness_success_response_authenticates_exact_bytes() {
        let key_hex = "42".repeat(32);
        let key = hex::decode(&key_hex).unwrap();
        let auth = ServiceAuth::new("account-publisher", &key_hex).unwrap();
        let request = AuthorizedRequest {
            caller: "account-publisher".to_string(),
            nonce: "n".repeat(48),
            nonce_expires_at: Utc::now() + chrono::Duration::minutes(1),
        };
        let response =
            signed_readiness_response(&auth, &request, &serde_json::json!({"stored": true}))
                .unwrap();
        assert_eq!(response.status(), actix_web::http::StatusCode::ACCEPTED);
        let signature = response
            .headers()
            .get("X-RTC-Response-Signature")
            .unwrap()
            .to_str()
            .unwrap()
            .to_string();
        let body = to_bytes(response.into_body()).await.unwrap();
        assert_eq!(body.as_ref(), br#"{"stored":true}"#);

        let canonical = format!(
            "RESPONSE\n{READINESS_PATH}\n{}\n{}\n{}\n{}",
            request.caller,
            request.nonce,
            actix_web::http::StatusCode::ACCEPTED.as_u16(),
            hex::encode(Sha256::digest(&body))
        );
        let mut mac = Hmac::<Sha256>::new_from_slice(&key).unwrap();
        mac.update(canonical.as_bytes());
        mac.verify_slice(&hex::decode(signature).unwrap()).unwrap();
    }
}
