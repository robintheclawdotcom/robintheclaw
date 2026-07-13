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

#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SignedLighterTransaction {
    pub intent_id: String,
    pub tx_type: u8,
    pub tx_hash: String,
    pub tx_info: serde_json::Value,
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
pub struct LighterSubmission {
    pub code: i32,
    pub message: Option<String>,
    pub tx_hash: String,
    pub predicted_execution_time_ms: i64,
    pub volume_quota_remaining: i64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct RobinhoodExecuteRequest {
    pub request_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub replaces_request_id: Option<String>,
    pub intent: RobinhoodSpotIntent,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct RobinhoodSpotIntent {
    pub id: String,
    pub stock_token: String,
    pub side: String,
    pub amount_in: String,
    pub min_amount_out: String,
    pub deadline: u64,
    pub config_version: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
pub struct RobinhoodSubmission {
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
    #[error("signer returned an invalid response")]
    InvalidSignerResponse,
    #[error("Lighter submission outcome is ambiguous")]
    AmbiguousLighterSubmission,
    #[error("Lighter rejected transaction with code {0}")]
    LighterRejected(i32),
    #[error("Lighter returned an inconsistent transaction hash")]
    LighterHashMismatch,
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
        if signed.intent_id != request.intent_id
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
            let code = serde_json::from_slice::<ResultCode>(&body)
                .map(|result| result.code)
                .unwrap_or(i32::from(status.as_u16()));
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
        if submission.request_id != request.request_id
            || submission.intent_id != request.intent.id
            || !valid_hash(&submission.tx_hash)
            || submission.status != "submitted"
        {
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
            .header("X-RTC-Nonce", nonce)
            .header("X-RTC-Signature", signature)
            .body(body)
            .send()
            .await
            .map_err(|_| SignerClientError::SignerTransport)?;
        let status = response.status();
        let body = read_limited(response)
            .await
            .map_err(|_| SignerClientError::InvalidSignerResponse)?;
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

async fn read_limited(mut response: Response) -> Result<Vec<u8>, reqwest::Error> {
    let mut body = Vec::new();
    while let Some(chunk) = response.chunk().await? {
        if body.len().saturating_add(chunk.len()) > MAX_RESPONSE_BYTES {
            body.clear();
            return Ok(body);
        }
        body.extend_from_slice(&chunk);
    }
    Ok(body)
}

fn valid_hash(value: &str) -> bool {
    value.len() == 66
        && value.starts_with("0x")
        && value[2..].bytes().all(|byte| byte.is_ascii_hexdigit())
}

#[derive(Deserialize)]
struct ResultCode {
    code: i32,
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{
        body::Bytes,
        extract::State,
        http::{HeaderMap, StatusCode},
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
        let app = Router::new()
            .route(
                "/v1/sign/create-order",
                post(
                    |State(state): State<CapturedRequest>,
                     headers: HeaderMap,
                     body: Bytes| async move {
                        *state.lock().unwrap() = Some((headers, body));
                        Json(serde_json::json!({
                            "intentId": "intent-1",
                            "txType": 14,
                            "txHash": HASH,
                            "txInfo": {"Nonce": 1}
                        }))
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
                intent_id: "intent-1".into(),
                tx_type: 14,
                tx_hash: HASH.into(),
                tx_info: serde_json::json!({"Nonce": 1}),
            })
            .await;
        assert_eq!(result, Err(SignerClientError::LighterRejected(21100)));
    }

    fn lighter_request() -> LighterCreateOrderRequest {
        LighterCreateOrderRequest {
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
            order_expiry_ms: 1_800_000_000_000,
            transaction: LighterTransactionOptions {
                nonce: 1,
                expires_at_ms: 1_800_000_000_000,
            },
        }
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
