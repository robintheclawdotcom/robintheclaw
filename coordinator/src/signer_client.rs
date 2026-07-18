use hmac::{Hmac, Mac};
use reqwest::{header, Client, Method, Response};
use serde::{de::DeserializeOwned, Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::time::{SystemTime, UNIX_EPOCH};
use thiserror::Error;
use uuid::Uuid;

const MAX_RESPONSE_BYTES: usize = 64 << 10;

#[derive(Clone)]
pub struct SignerClients {
    client: Client,
    caller_id: String,
    lighter_signer_url: String,
    robinhood_signer_url: String,
    lighter_api_url: String,
    lighter_hmac_key: [u8; 32],
    robinhood_hmac_key: [u8; 32],
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LighterCreateOrderRequest {
    pub execution_account_id: String,
    pub intent_id: String,
    pub market_index: i16,
    pub client_order_index: i64,
    pub base_amount: i64,
    pub price: u32,
    pub is_ask: bool,
    pub order_type: u8,
    pub time_in_force: u8,
    pub reduce_only: bool,
    pub trigger_price: u32,
    pub order_expiry_ms: i64,
    pub transaction: LighterTransactionOptions,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LighterTransactionOptions {
    pub nonce: i64,
    pub expires_at_ms: i64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SignedLighterTransaction {
    pub execution_account_id: String,
    pub account_index: i64,
    pub api_key_index: u8,
    pub intent_id: String,
    pub tx_type: u8,
    pub tx_hash: String,
    pub tx_info: serde_json::Value,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct LighterSubmission {
    pub code: i32,
    pub message: Option<String>,
    pub tx_hash: String,
    pub predicted_execution_time_ms: i64,
    pub volume_quota_remaining: i64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct RobinhoodExecuteRequest {
    pub execution_account_id: String,
    pub request_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub replaces_request_id: Option<String>,
    pub intent: RobinhoodSpotIntent,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct RobinhoodSpotIntent {
    pub id: String,
    pub stock_token: String,
    pub side: String,
    pub amount_in: String,
    pub min_amount_out: String,
    pub expected_ui_multiplier: String,
    pub min_oracle_round_id: String,
    pub deadline: u64,
    pub config_version: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct RobinhoodSubmission {
    pub execution_account_id: String,
    pub vault_address: String,
    pub signer_address: String,
    pub request_id: String,
    pub intent_id: String,
    pub tx_hash: String,
    pub nonce: u64,
    pub status: String,
}

#[derive(Debug, Error, PartialEq, Eq)]
pub enum SignerClientError {
    #[error("request encoding failed")]
    Encoding,
    #[error("signer request failed before acceptance")]
    SignerTransport,
    #[error("signer rejected request with status {0}")]
    SignerRejected(u16),
    #[error("Robinhood writer returned terminal journal state {0}")]
    RobinhoodJournalRejected(String),
    #[error("signer returned an invalid response")]
    InvalidSignerResponse,
    #[error("Lighter submission outcome is ambiguous")]
    AmbiguousLighterSubmission,
    #[error("Lighter rejected transaction with code {0}")]
    LighterRejected(i32),
    #[error("Lighter returned an inconsistent transaction hash")]
    LighterHashMismatch,
    #[error("Lighter nonce query failed")]
    LighterNonceQuery,
    #[error("system clock is unavailable")]
    Clock,
}

impl SignerClients {
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        client: Client,
        caller_id: String,
        lighter_signer_url: String,
        robinhood_signer_url: String,
        lighter_api_url: String,
        lighter_hmac_key: [u8; 32],
        robinhood_hmac_key: [u8; 32],
    ) -> Self {
        Self {
            client,
            caller_id,
            lighter_signer_url: lighter_signer_url.trim_end_matches('/').into(),
            robinhood_signer_url: robinhood_signer_url.trim_end_matches('/').into(),
            lighter_api_url: lighter_api_url.trim_end_matches('/').into(),
            lighter_hmac_key,
            robinhood_hmac_key,
        }
    }

    pub async fn sign_lighter_create_order(
        &self,
        request: &LighterCreateOrderRequest,
    ) -> Result<SignedLighterTransaction, SignerClientError> {
        let signed: SignedLighterTransaction = self
            .post_signer(
                &self.lighter_signer_url,
                "/v1/sign/create-order",
                &self.lighter_hmac_key,
                request,
            )
            .await?;
        if signed.execution_account_id != request.execution_account_id
            || signed.account_index <= 0
            || !(4..=254).contains(&signed.api_key_index)
            || signed.intent_id != request.intent_id
            || !valid_hash(&signed.tx_hash)
            || !signed.tx_info.is_object()
        {
            return Err(SignerClientError::InvalidSignerResponse);
        }
        Ok(signed)
    }

    pub async fn broadcast_lighter(
        &self,
        signed: &SignedLighterTransaction,
    ) -> Result<LighterSubmission, SignerClientError> {
        let tx_info =
            serde_json::to_string(&signed.tx_info).map_err(|_| SignerClientError::Encoding)?;
        let response = self
            .client
            .post(format!("{}/api/v1/sendTx", self.lighter_api_url))
            .form(&[
                ("tx_type", signed.tx_type.to_string()),
                ("tx_info", tx_info),
                ("price_protection", "true".into()),
            ])
            .send()
            .await
            .map_err(|_| SignerClientError::AmbiguousLighterSubmission)?;
        let status = response.status();
        let body = read_limited(response)
            .await
            .map_err(|_| SignerClientError::AmbiguousLighterSubmission)?;
        if !status.is_success() {
            if status.is_server_error()
                || status == reqwest::StatusCode::REQUEST_TIMEOUT
                || status == reqwest::StatusCode::TOO_MANY_REQUESTS
            {
                return Err(SignerClientError::AmbiguousLighterSubmission);
            }
            let code = serde_json::from_slice::<ResultCode>(&body)
                .map(|result| result.code)
                .map_err(|_| SignerClientError::AmbiguousLighterSubmission)?;
            return Err(SignerClientError::LighterRejected(code));
        }
        let submission: LighterSubmission = serde_json::from_slice(&body)
            .map_err(|_| SignerClientError::AmbiguousLighterSubmission)?;
        if submission.code != 200 {
            return Err(SignerClientError::LighterRejected(submission.code));
        }
        if !valid_hash(&submission.tx_hash)
            || !submission.tx_hash.eq_ignore_ascii_case(&signed.tx_hash)
        {
            return Err(SignerClientError::LighterHashMismatch);
        }
        Ok(submission)
    }

    pub async fn fetch_lighter_nonce(
        &self,
        account_index: i64,
        api_key_index: u8,
    ) -> Result<i64, SignerClientError> {
        if account_index <= 0 || !(4..=254).contains(&api_key_index) {
            return Err(SignerClientError::LighterNonceQuery);
        }
        let response = self
            .client
            .get(format!("{}/api/v1/nextNonce", self.lighter_api_url))
            .query(&[
                ("account_index", account_index.to_string()),
                ("api_key_index", api_key_index.to_string()),
            ])
            .send()
            .await
            .map_err(|_| SignerClientError::LighterNonceQuery)?;
        let status = response.status();
        let body = read_limited(response)
            .await
            .map_err(|_| SignerClientError::LighterNonceQuery)?;
        if !status.is_success() {
            return Err(SignerClientError::LighterNonceQuery);
        }
        let result: NextNonce =
            serde_json::from_slice(&body).map_err(|_| SignerClientError::LighterNonceQuery)?;
        if result.code != 200 || result.nonce < 0 {
            return Err(SignerClientError::LighterNonceQuery);
        }
        Ok(result.nonce)
    }

    pub async fn execute_robinhood_spot(
        &self,
        request: &RobinhoodExecuteRequest,
    ) -> Result<RobinhoodSubmission, SignerClientError> {
        let submission: RobinhoodSubmission = self
            .post_signer(
                &self.robinhood_signer_url,
                "/v1/spot-intents",
                &self.robinhood_hmac_key,
                request,
            )
            .await?;
        if submission.execution_account_id != request.execution_account_id
            || !valid_address(&submission.vault_address)
            || !valid_address(&submission.signer_address)
            || submission.vault_address == submission.signer_address
            || submission.request_id != request.request_id
            || submission.intent_id != request.intent.id
            || !valid_hash(&submission.tx_hash)
        {
            return Err(SignerClientError::InvalidSignerResponse);
        }
        if rejected_robinhood_submission(&submission.status) {
            return Err(SignerClientError::RobinhoodJournalRejected(
                submission.status,
            ));
        }
        if !accepted_robinhood_submission(&submission.status) {
            return Err(SignerClientError::InvalidSignerResponse);
        }
        Ok(submission)
    }

    async fn post_signer<T, R>(
        &self,
        base_url: &str,
        path: &str,
        key: &[u8; 32],
        payload: &T,
    ) -> Result<R, SignerClientError>
    where
        T: Serialize,
        R: DeserializeOwned,
    {
        let body = serde_json::to_vec(payload).map_err(|_| SignerClientError::Encoding)?;
        let timestamp = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| SignerClientError::Clock)?
            .as_secs()
            .to_string();
        let nonce = Uuid::new_v4().simple().to_string();
        let signature = sign_request(
            key,
            Method::POST.as_str(),
            path,
            &self.caller_id,
            &timestamp,
            &nonce,
            &body,
        );
        let response = self
            .client
            .post(format!("{base_url}{path}"))
            .header(header::CONTENT_TYPE, "application/json")
            .header("X-RTC-Caller", &self.caller_id)
            .header("X-RTC-Timestamp", timestamp)
            .header("X-RTC-Nonce", &nonce)
            .header("X-RTC-Signature", signature)
            .body(body)
            .send()
            .await
            .map_err(|_| SignerClientError::SignerTransport)?;
        let (status, body) =
            read_authenticated_response(response, path, &self.caller_id, &nonce, key).await?;
        if !status.is_success() {
            return Err(SignerClientError::SignerRejected(status.as_u16()));
        }
        serde_json::from_slice(&body).map_err(|_| SignerClientError::InvalidSignerResponse)
    }
}

fn sign_request(
    key: &[u8; 32],
    method: &str,
    path: &str,
    caller: &str,
    timestamp: &str,
    nonce: &str,
    body: &[u8],
) -> String {
    let body_digest = Sha256::digest(body);
    let canonical = format!(
        "{method}\n{path}\n{caller}\n{timestamp}\n{nonce}\n{}",
        hex::encode(body_digest)
    );
    let mut mac = Hmac::<Sha256>::new_from_slice(key).expect("fixed-length HMAC key");
    mac.update(canonical.as_bytes());
    hex::encode(mac.finalize().into_bytes())
}

async fn read_authenticated_response(
    response: Response,
    path: &str,
    caller: &str,
    nonce: &str,
    key: &[u8; 32],
) -> Result<(reqwest::StatusCode, Vec<u8>), SignerClientError> {
    let mut signatures = response
        .headers()
        .get_all("X-RTC-Response-Signature")
        .iter();
    let encoded = signatures
        .next()
        .ok_or(SignerClientError::InvalidSignerResponse)?;
    if signatures.next().is_some() {
        return Err(SignerClientError::InvalidSignerResponse);
    }
    let provided =
        hex::decode(encoded.as_bytes()).map_err(|_| SignerClientError::InvalidSignerResponse)?;
    if provided.len() != Sha256::output_size() {
        return Err(SignerClientError::InvalidSignerResponse);
    }

    let status = response.status();
    let body = read_limited(response)
        .await
        .map_err(|_| SignerClientError::InvalidSignerResponse)?;
    let digest = Sha256::digest(&body);
    let canonical = format!(
        "RESPONSE\n{path}\n{caller}\n{nonce}\n{}\n{}",
        status.as_u16(),
        hex::encode(digest)
    );
    let mut mac = Hmac::<Sha256>::new_from_slice(key).expect("fixed-length HMAC key");
    mac.update(canonical.as_bytes());
    mac.verify_slice(&provided)
        .map_err(|_| SignerClientError::InvalidSignerResponse)?;
    Ok((status, body))
}

enum ResponseReadError {
    Transport,
    TooLarge,
}

async fn read_limited(mut response: Response) -> Result<Vec<u8>, ResponseReadError> {
    let mut body = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| ResponseReadError::Transport)?
    {
        if body.len().saturating_add(chunk.len()) > MAX_RESPONSE_BYTES {
            return Err(ResponseReadError::TooLarge);
        }
        body.extend_from_slice(&chunk);
    }
    Ok(body)
}

fn valid_hash(value: &str) -> bool {
    value.len() == 66
        && value.starts_with("0x")
        && value[2..]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        && value[2..].bytes().any(|byte| byte != b'0')
}

fn valid_address(value: &str) -> bool {
    value.len() == 42
        && value.starts_with("0x")
        && value[2..]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        && value[2..].bytes().any(|byte| byte != b'0')
}

fn accepted_robinhood_submission(status: &str) -> bool {
    matches!(
        status,
        "signed"
            | "submitted"
            | "soft_confirmed"
            | "l1_posted"
            | "ethereum_final"
            | "ambiguous"
            | "replaced"
            | "superseded"
            | "quarantined"
    )
}

fn rejected_robinhood_submission(status: &str) -> bool {
    status == "reverted"
}

#[derive(Deserialize)]
struct ResultCode {
    code: i32,
}

#[derive(Deserialize)]
struct NextNonce {
    code: i32,
    nonce: i64,
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{
        body::{Body, Bytes},
        extract::State,
        http::{HeaderMap, StatusCode},
        response::Response as AxumResponse,
        routing::post,
        Json, Router,
    };
    use std::sync::{Arc, Mutex};
    use tokio::net::TcpListener;

    const HASH: &str = "0x1111111111111111111111111111111111111111111111111111111111111111";
    type CapturedRequest = Arc<Mutex<Option<(HeaderMap, Bytes)>>>;

    #[test]
    fn signature_binds_exact_body() {
        let key = [7; 32];
        let signature = sign_request(
            &key,
            "POST",
            "/v1/sign/create-order",
            "execution-coordinator",
            "1800000000",
            "nnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnn",
            br#"{"intentId":"intent-1"}"#,
        );
        let altered = sign_request(
            &key,
            "POST",
            "/v1/sign/create-order",
            "execution-coordinator",
            "1800000000",
            "nnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnn",
            br#"{"intentId":"intent-2"}"#,
        );
        assert_ne!(signature, altered);
        assert_eq!(signature.len(), 64);
    }

    #[tokio::test]
    async fn signer_request_is_authenticated() {
        let key = [5; 32];
        let captured = Arc::new(Mutex::new(None));
        let app =
            Router::new()
                .route(
                    "/v1/sign/create-order",
                    post(
                        move |State(state): State<CapturedRequest>,
                              headers: HeaderMap,
                              body: Bytes| async move {
                            let response_headers = headers.clone();
                            *state.lock().unwrap() = Some((headers, body));
                            authenticated_json_response(
                                &key,
                                "/v1/sign/create-order",
                                &response_headers,
                                StatusCode::OK,
                                serde_json::json!({
                                "executionAccountId": "account-canary-1",
                                "accountIndex": 7,
                                "apiKeyIndex": 4,
                                "intentId": "intent-1",
                                "txType": 14,
                                "txHash": HASH,
                                "txInfo": {"Nonce": 1}
                                }),
                            )
                        },
                    ),
                )
                .with_state(captured.clone());
        let base_url = serve(app).await;
        let clients = clients(&base_url, &base_url, key, [6; 32]);
        let request = lighter_request();
        clients.sign_lighter_create_order(&request).await.unwrap();

        let (headers, body) = captured.lock().unwrap().take().unwrap();
        let timestamp = headers["X-RTC-Timestamp"].to_str().unwrap();
        let nonce = headers["X-RTC-Nonce"].to_str().unwrap();
        let expected = sign_request(
            &key,
            "POST",
            "/v1/sign/create-order",
            "execution-coordinator",
            timestamp,
            nonce,
            &body,
        );
        assert_eq!(headers["X-RTC-Signature"], expected);
    }

    #[tokio::test]
    async fn lighter_broadcast_requires_matching_hash() {
        let app = Router::new().route(
            "/api/v1/sendTx",
            post(|| async {
                Json(serde_json::json!({
                    "code": 200,
                    "tx_hash": "0x2222222222222222222222222222222222222222222222222222222222222222",
                    "predicted_execution_time_ms": 1,
                    "volume_quota_remaining": 10
                }))
            }),
        );
        let base_url = serve(app).await;
        let clients = clients(&base_url, &base_url, [5; 32], [6; 32]);
        let result = clients
            .broadcast_lighter(&SignedLighterTransaction {
                execution_account_id: "account-canary-1".into(),
                account_index: 7,
                api_key_index: 4,
                intent_id: "intent-1".into(),
                tx_type: 14,
                tx_hash: HASH.into(),
                tx_info: serde_json::json!({"Nonce": 1}),
            })
            .await;
        assert_eq!(result, Err(SignerClientError::LighterHashMismatch));
    }

    #[tokio::test]
    async fn explicit_lighter_rejection_is_not_ambiguous() {
        let app = Router::new().route(
            "/api/v1/sendTx",
            post(|| async {
                (
                    StatusCode::BAD_REQUEST,
                    Json(serde_json::json!({"code": 21100})),
                )
            }),
        );
        let base_url = serve(app).await;
        let clients = clients(&base_url, &base_url, [5; 32], [6; 32]);
        let result = clients
            .broadcast_lighter(&SignedLighterTransaction {
                execution_account_id: "account-canary-1".into(),
                account_index: 7,
                api_key_index: 4,
                intent_id: "intent-1".into(),
                tx_type: 14,
                tx_hash: HASH.into(),
                tx_info: serde_json::json!({"Nonce": 1}),
            })
            .await;
        assert_eq!(result, Err(SignerClientError::LighterRejected(21100)));
    }

    #[tokio::test]
    async fn lighter_server_failure_is_ambiguous() {
        let app = Router::new().route(
            "/api/v1/sendTx",
            post(|| async {
                (
                    StatusCode::SERVICE_UNAVAILABLE,
                    Json(serde_json::json!({"code": 503})),
                )
            }),
        );
        let base_url = serve(app).await;
        let clients = clients(&base_url, &base_url, [5; 32], [6; 32]);
        let result = clients
            .broadcast_lighter(&SignedLighterTransaction {
                execution_account_id: "account-canary-1".into(),
                account_index: 7,
                api_key_index: 4,
                intent_id: "intent-1".into(),
                tx_type: 14,
                tx_hash: HASH.into(),
                tx_info: serde_json::json!({"Nonce": 1}),
            })
            .await;
        assert_eq!(result, Err(SignerClientError::AmbiguousLighterSubmission));
    }

    #[tokio::test]
    async fn next_nonce_query_is_scoped_to_account_and_key() {
        let captured = Arc::new(Mutex::new(None));
        let app = Router::new()
            .route(
                "/api/v1/nextNonce",
                axum::routing::get(
                    |State(state): State<Arc<Mutex<Option<String>>>>,
                     request: axum::extract::Request| async move {
                        *state.lock().unwrap() = request.uri().query().map(str::to_owned);
                        Json(serde_json::json!({"code": 200, "nonce": 42}))
                    },
                ),
            )
            .with_state(captured.clone());
        let base_url = serve(app).await;
        let clients = clients(&base_url, &base_url, [5; 32], [6; 32]);
        assert_eq!(clients.fetch_lighter_nonce(7, 4).await, Ok(42));
        let query = captured.lock().unwrap().take().unwrap();
        assert!(query.contains("account_index=7"));
        assert!(query.contains("api_key_index=4"));
        assert_eq!(
            clients.fetch_lighter_nonce(7, 3).await,
            Err(SignerClientError::LighterNonceQuery)
        );
    }

    #[tokio::test]
    async fn robinhood_accepts_states_that_require_chain_reconciliation() {
        for status in [
            "signed",
            "submitted",
            "soft_confirmed",
            "l1_posted",
            "ethereum_final",
            "ambiguous",
            "replaced",
            "superseded",
            "quarantined",
        ] {
            let app = robinhood_app(StatusCode::ACCEPTED, status, [6; 32]);
            let base_url = serve(app).await;
            let clients = clients(&base_url, &base_url, [5; 32], [6; 32]);
            let submission = clients
                .execute_robinhood_spot(&robinhood_request())
                .await
                .unwrap();
            assert_eq!(submission.status, status);
        }
    }

    #[tokio::test]
    async fn robinhood_rejects_terminal_failure_states() {
        let app = robinhood_app(StatusCode::ACCEPTED, "reverted", [6; 32]);
        let base_url = serve(app).await;
        let clients = clients(&base_url, &base_url, [5; 32], [6; 32]);
        let result = clients.execute_robinhood_spot(&robinhood_request()).await;
        assert_eq!(
            result,
            Err(SignerClientError::RobinhoodJournalRejected(
                "reverted".into()
            ))
        );
    }

    #[test]
    fn transaction_hashes_must_be_nonzero() {
        assert!(valid_hash(HASH));
        assert!(!valid_hash(
            "0x0000000000000000000000000000000000000000000000000000000000000000"
        ));
    }

    #[tokio::test]
    async fn robinhood_rejects_unknown_journal_state() {
        let app = robinhood_app(StatusCode::ACCEPTED, "unknown", [6; 32]);
        let base_url = serve(app).await;
        let clients = clients(&base_url, &base_url, [5; 32], [6; 32]);
        let result = clients.execute_robinhood_spot(&robinhood_request()).await;
        assert_eq!(result, Err(SignerClientError::InvalidSignerResponse));
    }

    #[tokio::test]
    async fn robinhood_conflict_is_a_deterministic_signer_rejection() {
        let app = Router::new().route(
            "/v1/spot-intents",
            post(|headers: HeaderMap| async move {
                authenticated_response(
                    &[6; 32],
                    "/v1/spot-intents",
                    &headers,
                    StatusCode::CONFLICT,
                    Vec::new(),
                )
            }),
        );
        let base_url = serve(app).await;
        let clients = clients(&base_url, &base_url, [5; 32], [6; 32]);
        let result = clients.execute_robinhood_spot(&robinhood_request()).await;
        assert_eq!(result, Err(SignerClientError::SignerRejected(409)));
    }

    #[tokio::test]
    async fn signer_responses_fail_closed_on_authentication_tampering() {
        for mutation in [
            ResponseMutation::Missing,
            ResponseMutation::Body,
            ResponseMutation::Status,
            ResponseMutation::Path,
            ResponseMutation::Nonce,
            ResponseMutation::SignerSubstitution,
        ] {
            let app = tampered_lighter_app(mutation);
            let base_url = serve(app).await;
            let clients = clients(&base_url, &base_url, [5; 32], [6; 32]);
            let result = clients.sign_lighter_create_order(&lighter_request()).await;
            assert_eq!(
                result,
                Err(SignerClientError::InvalidSignerResponse),
                "accepted {mutation:?}"
            );
        }
    }

    #[tokio::test]
    async fn robinhood_response_cannot_be_substituted_for_lighter() {
        let app = robinhood_app(StatusCode::ACCEPTED, "submitted", [5; 32]);
        let base_url = serve(app).await;
        let clients = clients(&base_url, &base_url, [5; 32], [6; 32]);
        assert_eq!(
            clients.execute_robinhood_spot(&robinhood_request()).await,
            Err(SignerClientError::InvalidSignerResponse)
        );
    }

    fn lighter_request() -> LighterCreateOrderRequest {
        LighterCreateOrderRequest {
            execution_account_id: "account-canary-1".into(),
            intent_id: "intent-1".into(),
            market_index: 101,
            client_order_index: 1,
            base_amount: 100,
            price: 200,
            is_ask: true,
            order_type: 0,
            time_in_force: 0,
            reduce_only: false,
            trigger_price: 0,
            order_expiry_ms: 0,
            transaction: LighterTransactionOptions {
                nonce: 1,
                expires_at_ms: 1_800_000_000_000,
            },
        }
    }

    fn robinhood_request() -> RobinhoodExecuteRequest {
        RobinhoodExecuteRequest {
            execution_account_id: "account-canary-1".into(),
            request_id: "action-1".into(),
            replaces_request_id: None,
            intent: RobinhoodSpotIntent {
                id: "0x1111111111111111111111111111111111111111111111111111111111111111".into(),
                stock_token: "0x0000000000000000000000000000000000000001".into(),
                side: "buy_spot".into(),
                amount_in: "25000000".into(),
                min_amount_out: "1990000".into(),
                expected_ui_multiplier: "500000000000000000".into(),
                min_oracle_round_id: "1".into(),
                deadline: 1_800_000_001,
                config_version: 1,
            },
        }
    }

    fn robinhood_app(status_code: StatusCode, status: &str, key: [u8; 32]) -> Router {
        let status = status.to_owned();
        Router::new().route(
            "/v1/spot-intents",
            post(move |headers: HeaderMap| {
                let status = status.clone();
                async move {
                    authenticated_json_response(
                        &key,
                        "/v1/spot-intents",
                        &headers,
                        status_code,
                        serde_json::json!({
                            "execution_account_id": "account-canary-1",
                            "vault_address": "0x0000000000000000000000000000000000000002",
                            "signer_address": "0x0000000000000000000000000000000000000003",
                            "request_id": "action-1",
                            "intent_id": "0x1111111111111111111111111111111111111111111111111111111111111111",
                            "tx_hash": HASH,
                            "nonce": 7,
                            "status": status,
                        }),
                    )
                }
            }),
        )
    }

    #[derive(Clone, Copy, Debug)]
    enum ResponseMutation {
        Missing,
        Body,
        Status,
        Path,
        Nonce,
        SignerSubstitution,
    }

    fn tampered_lighter_app(mutation: ResponseMutation) -> Router {
        Router::new().route(
            "/v1/sign/create-order",
            post(move |headers: HeaderMap| async move {
                let actual_body = serde_json::to_vec(&serde_json::json!({
                    "executionAccountId": "account-canary-1",
                    "accountIndex": 7,
                    "apiKeyIndex": 4,
                    "intentId": "intent-1",
                    "txType": 14,
                    "txHash": HASH,
                    "txInfo": {"Nonce": 1}
                }))
                .unwrap();
                let signed_body = if matches!(mutation, ResponseMutation::Body) {
                    b"{}".to_vec()
                } else {
                    actual_body.clone()
                };
                let signed_status = if matches!(mutation, ResponseMutation::Status) {
                    StatusCode::CREATED
                } else {
                    StatusCode::OK
                };
                let path = if matches!(mutation, ResponseMutation::Path) {
                    "/v1/spot-intents"
                } else {
                    "/v1/sign/create-order"
                };
                let nonce = if matches!(mutation, ResponseMutation::Nonce) {
                    "substituted-response-nonce"
                } else {
                    header_value(&headers, "X-RTC-Nonce")
                };
                let key = if matches!(mutation, ResponseMutation::SignerSubstitution) {
                    [6; 32]
                } else {
                    [5; 32]
                };
                response_with_signature(
                    &key,
                    path,
                    "execution-coordinator",
                    nonce,
                    signed_status,
                    StatusCode::OK,
                    signed_body,
                    actual_body,
                    !matches!(mutation, ResponseMutation::Missing),
                )
            }),
        )
    }

    fn authenticated_json_response(
        key: &[u8; 32],
        path: &str,
        headers: &HeaderMap,
        status: StatusCode,
        value: serde_json::Value,
    ) -> AxumResponse {
        authenticated_response(
            key,
            path,
            headers,
            status,
            serde_json::to_vec(&value).unwrap(),
        )
    }

    fn authenticated_response(
        key: &[u8; 32],
        path: &str,
        headers: &HeaderMap,
        status: StatusCode,
        body: Vec<u8>,
    ) -> AxumResponse {
        response_with_signature(
            key,
            path,
            "execution-coordinator",
            header_value(headers, "X-RTC-Nonce"),
            status,
            status,
            body.clone(),
            body,
            true,
        )
    }

    #[allow(clippy::too_many_arguments)]
    fn response_with_signature(
        key: &[u8; 32],
        path: &str,
        caller: &str,
        nonce: &str,
        signed_status: StatusCode,
        actual_status: StatusCode,
        signed_body: Vec<u8>,
        actual_body: Vec<u8>,
        include_signature: bool,
    ) -> AxumResponse {
        let digest = Sha256::digest(&signed_body);
        let canonical = format!(
            "RESPONSE\n{path}\n{caller}\n{nonce}\n{}\n{}",
            signed_status.as_u16(),
            hex::encode(digest)
        );
        let mut mac = Hmac::<Sha256>::new_from_slice(key).unwrap();
        mac.update(canonical.as_bytes());
        let mut builder = AxumResponse::builder()
            .status(actual_status)
            .header(header::CONTENT_TYPE, "application/json");
        if include_signature {
            builder = builder.header(
                "X-RTC-Response-Signature",
                hex::encode(mac.finalize().into_bytes()),
            );
        }
        builder.body(Body::from(actual_body)).unwrap()
    }

    fn header_value<'a>(headers: &'a HeaderMap, name: &str) -> &'a str {
        headers
            .get(name)
            .and_then(|value| value.to_str().ok())
            .unwrap_or("")
    }

    fn clients(
        signer_url: &str,
        lighter_url: &str,
        lighter_key: [u8; 32],
        robinhood_key: [u8; 32],
    ) -> SignerClients {
        SignerClients::new(
            Client::new(),
            "execution-coordinator".into(),
            signer_url.into(),
            signer_url.into(),
            lighter_url.into(),
            lighter_key,
            robinhood_key,
        )
    }

    async fn serve(app: Router) -> String {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
        format!("http://{address}")
    }
}
