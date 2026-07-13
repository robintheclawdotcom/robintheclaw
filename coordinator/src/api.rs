use crate::{
    store::{
        AccountCommandRequest, AccountCommandStatusRequest, ExitRequest, NewAccountSnapshot,
        NewMarketQuote, NewVenueEvent, RecoveryRequest, StoreError,
    },
    AppState,
};
use axum::{
    body::Bytes,
    extract::State,
    http::{HeaderMap, StatusCode},
    response::IntoResponse,
    routing::{get, post},
    Json, Router,
};
use execution::PairIntent;
use hmac::{Hmac, Mac};
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::{sync::Arc, time::SystemTime};

pub fn routes(state: Arc<AppState>) -> Router {
    Router::new()
        .route("/livez", get(livez))
        .route("/readyz", get(readyz))
        .route("/v1/intents", post(create_intent))
        .route("/v1/exits", post(request_exit))
        .route("/v1/recoveries", post(request_recovery))
        .route("/v1/venue-events", post(record_venue_event))
        .route("/v1/account-snapshots", post(record_account_snapshot))
        .route("/v1/account-commands", post(submit_account_command))
        .route("/v1/account-command-status", post(account_command_status))
        .route("/v1/market-quotes", post(record_market_quote))
        .layer(axum::extract::DefaultBodyLimit::max(64 << 10))
        .with_state(state)
}

async fn submit_account_command(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    body: Bytes,
) -> impl IntoResponse {
    if let Err((status, message)) = authorize(
        &state,
        AuthScope::AccountCommand,
        "/v1/account-commands",
        &headers,
        &body,
    )
    .await
    {
        return error(status, message);
    }
    let Some(store) = &state.store else {
        return error(StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled");
    };
    let request: AccountCommandRequest = match serde_json::from_slice(&body) {
        Ok(request) => request,
        Err(_) => return error(StatusCode::BAD_REQUEST, "invalid request"),
    };
    let now_ms = match current_time_ms() {
        Ok(value) => value,
        Err(_) => return error(StatusCode::SERVICE_UNAVAILABLE, "clock unavailable"),
    };
    match store.submit_account_command(&request, now_ms).await {
        Ok(response) => {
            let status = if response.status == "completed" {
                StatusCode::OK
            } else {
                StatusCode::ACCEPTED
            };
            (status, Json(serde_json::json!(response))).into_response()
        }
        Err(error) => store_error_response(error),
    }
}

async fn account_command_status(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    body: Bytes,
) -> impl IntoResponse {
    if let Err((status, message)) = authorize(
        &state,
        AuthScope::AccountCommand,
        "/v1/account-command-status",
        &headers,
        &body,
    )
    .await
    {
        return error(status, message);
    }
    let Some(store) = &state.store else {
        return error(StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled");
    };
    let request: AccountCommandStatusRequest = match serde_json::from_slice(&body) {
        Ok(request) => request,
        Err(_) => return error(StatusCode::BAD_REQUEST, "invalid request"),
    };
    let now_ms = match current_time_ms() {
        Ok(value) => value,
        Err(_) => return error(StatusCode::SERVICE_UNAVAILABLE, "clock unavailable"),
    };
    match store.account_command_status(&request, now_ms).await {
        Ok(response) => (StatusCode::OK, Json(serde_json::json!(response))).into_response(),
        Err(error) => store_error_response(error),
    }
}

async fn record_account_snapshot(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    body: Bytes,
) -> impl IntoResponse {
    if let Err((status, message)) = authorize(
        &state,
        AuthScope::AccountSnapshot,
        "/v1/account-snapshots",
        &headers,
        &body,
    )
    .await
    {
        return error(status, message);
    }
    let Some(store) = &state.store else {
        return error(StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled");
    };
    let snapshot: NewAccountSnapshot = match serde_json::from_slice(&body) {
        Ok(snapshot) => snapshot,
        Err(_) => return error(StatusCode::BAD_REQUEST, "invalid request"),
    };
    let now_ms = match current_time_ms().and_then(|value| i64::try_from(value).map_err(|_| ())) {
        Ok(value) => value,
        Err(_) => return error(StatusCode::SERVICE_UNAVAILABLE, "clock unavailable"),
    };
    if snapshot.received_at_ms.abs_diff(now_ms) > 5_000
        || snapshot.observed_at_ms.abs_diff(now_ms) > 5_000
        || snapshot.expires_at_ms <= now_ms
    {
        return error(
            StatusCode::CONFLICT,
            "account snapshot is stale or future-dated",
        );
    }
    match store.record_account_snapshot(&snapshot).await {
        Ok(true) => (
            StatusCode::ACCEPTED,
            Json(serde_json::json!({"status": "recorded"})),
        )
            .into_response(),
        Ok(false) => (
            StatusCode::OK,
            Json(serde_json::json!({"status": "duplicate"})),
        )
            .into_response(),
        Err(error) => store_error_response(error),
    }
}

async fn request_recovery(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    body: Bytes,
) -> impl IntoResponse {
    if let Err((status, message)) = authorize(
        &state,
        AuthScope::Recovery,
        "/v1/recoveries",
        &headers,
        &body,
    )
    .await
    {
        return error(status, message);
    }
    let Some(store) = &state.store else {
        return error(StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled");
    };
    let request: RecoveryRequest = match serde_json::from_slice(&body) {
        Ok(request) => request,
        Err(_) => return error(StatusCode::BAD_REQUEST, "invalid request"),
    };
    let now_ms = match current_time_ms() {
        Ok(value) => value,
        Err(_) => return error(StatusCode::SERVICE_UNAVAILABLE, "clock unavailable"),
    };
    match store.request_recovery(&request, now_ms).await {
        Ok(saga) => (StatusCode::ACCEPTED, Json(serde_json::json!(saga))).into_response(),
        Err(error) => store_error_response(error),
    }
}

async fn request_exit(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    body: Bytes,
) -> impl IntoResponse {
    if let Err((status, message)) =
        authorize(&state, AuthScope::Exit, "/v1/exits", &headers, &body).await
    {
        return error(status, message);
    }
    let Some(store) = &state.store else {
        return error(StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled");
    };
    let request: ExitRequest = match serde_json::from_slice(&body) {
        Ok(request) => request,
        Err(_) => return error(StatusCode::BAD_REQUEST, "invalid request"),
    };
    let now_ms = match current_time_ms() {
        Ok(value) => value,
        Err(_) => return error(StatusCode::SERVICE_UNAVAILABLE, "clock unavailable"),
    };
    match store.request_exit(&request, now_ms).await {
        Ok(saga) => (StatusCode::ACCEPTED, Json(serde_json::json!(saga))).into_response(),
        Err(error) => store_error_response(error),
    }
}

async fn record_market_quote(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    body: Bytes,
) -> impl IntoResponse {
    if let Err((status, message)) = authorize(
        &state,
        AuthScope::MarketQuote,
        "/v1/market-quotes",
        &headers,
        &body,
    )
    .await
    {
        return error(status, message);
    }
    let Some(store) = &state.store else {
        return error(StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled");
    };
    let quote: NewMarketQuote = match serde_json::from_slice(&body) {
        Ok(quote) => quote,
        Err(_) => return error(StatusCode::BAD_REQUEST, "invalid request"),
    };
    let now_ms = match current_time_ms().and_then(|value| i64::try_from(value).map_err(|_| ())) {
        Ok(value) => value,
        Err(_) => return error(StatusCode::SERVICE_UNAVAILABLE, "clock unavailable"),
    };
    if quote.received_at_ms.abs_diff(now_ms) > 60_000
        || quote.publisher_at_ms.abs_diff(now_ms) > 120_000
        || quote.received_at_ms.saturating_sub(quote.publisher_at_ms) > 60_000
        || quote.publisher_at_ms > quote.received_at_ms.saturating_add(30_000)
        || quote.expires_at_ms <= now_ms
        || quote.expires_at_ms.saturating_sub(quote.received_at_ms) > 30_000
    {
        return error(
            StatusCode::CONFLICT,
            "market quote is stale or future-dated",
        );
    }
    match store.record_market_quote(&quote).await {
        Ok(true) => (
            StatusCode::ACCEPTED,
            Json(serde_json::json!({"status": "recorded"})),
        )
            .into_response(),
        Ok(false) => (
            StatusCode::OK,
            Json(serde_json::json!({"status": "duplicate"})),
        )
            .into_response(),
        Err(error) => store_error_response(error),
    }
}

async fn livez() -> impl IntoResponse {
    (StatusCode::OK, Json(Status { status: "live" }))
}

async fn readyz(State(state): State<Arc<AppState>>) -> impl IntoResponse {
    if !state.config.enabled {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(Status { status: "disabled" }),
        );
    }
    let database_ready = match &state.store {
        Some(store) => store.ready().await,
        None => false,
    };
    let lighter_ready =
        signer_ready(&state.client, state.config.lighter_signer_url.as_deref()).await;
    let robinhood_ready =
        signer_ready(&state.client, state.config.robinhood_signer_url.as_deref()).await;
    if database_ready && lighter_ready && robinhood_ready {
        (StatusCode::OK, Json(Status { status: "ready" }))
    } else {
        (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(Status { status: "unready" }),
        )
    }
}

async fn create_intent(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    body: Bytes,
) -> impl IntoResponse {
    if let Err((status, message)) =
        authorize(&state, AuthScope::Intent, "/v1/intents", &headers, &body).await
    {
        return error(status, message);
    }
    let Some(store) = &state.store else {
        return error(StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled");
    };
    let intent: PairIntent = match serde_json::from_slice(&body) {
        Ok(intent) => intent,
        Err(_) => return error(StatusCode::BAD_REQUEST, "invalid request"),
    };
    let now_ms = match current_time_ms() {
        Ok(value) => value,
        Err(_) => return error(StatusCode::SERVICE_UNAVAILABLE, "clock unavailable"),
    };
    match store.create_intent(&intent, now_ms).await {
        Ok(saga) => (StatusCode::CREATED, Json(serde_json::json!(saga))).into_response(),
        Err(error) => store_error_response(error),
    }
}

async fn record_venue_event(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    body: Bytes,
) -> impl IntoResponse {
    if let Err((status, message)) = authorize(
        &state,
        AuthScope::VenueEvent,
        "/v1/venue-events",
        &headers,
        &body,
    )
    .await
    {
        return error(status, message);
    }
    let Some(store) = &state.store else {
        return error(StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled");
    };
    let event: NewVenueEvent = match serde_json::from_slice(&body) {
        Ok(event) => event,
        Err(_) => return error(StatusCode::BAD_REQUEST, "invalid request"),
    };
    let now_ms = match current_time_ms().and_then(|value| i64::try_from(value).map_err(|_| ())) {
        Ok(value) => value,
        Err(_) => return error(StatusCode::SERVICE_UNAVAILABLE, "clock unavailable"),
    };
    if event.received_at_ms > now_ms.saturating_add(30_000)
        || event.publisher_at_ms > now_ms.saturating_add(30_000)
        || event.publisher_at_ms > event.received_at_ms.saturating_add(30_000)
    {
        return error(StatusCode::CONFLICT, "venue event is stale or future-dated");
    }
    match store.record_venue_event(&event).await {
        Ok(true) => (
            StatusCode::ACCEPTED,
            Json(serde_json::json!({"status": "recorded"})),
        )
            .into_response(),
        Ok(false) => (
            StatusCode::OK,
            Json(serde_json::json!({"status": "duplicate"})),
        )
            .into_response(),
        Err(error) => store_error_response(error),
    }
}

#[derive(Clone, Copy)]
enum AuthScope {
    Intent,
    Exit,
    Recovery,
    VenueEvent,
    AccountSnapshot,
    MarketQuote,
    AccountCommand,
}

impl AuthScope {
    fn name(self) -> &'static str {
        match self {
            Self::Intent => "intent",
            Self::Exit => "exit",
            Self::Recovery => "recovery",
            Self::VenueEvent => "venue_event",
            Self::AccountSnapshot => "account_snapshot",
            Self::MarketQuote => "market_quote",
            Self::AccountCommand => "account_command",
        }
    }
}

async fn authorize(
    state: &AppState,
    scope: AuthScope,
    path: &str,
    headers: &HeaderMap,
    body: &[u8],
) -> Result<(), (StatusCode, &'static str)> {
    if !state.config.enabled {
        return Err((StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled"));
    }
    let Some(store) = &state.store else {
        return Err((StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled"));
    };
    let (key, caller) = match scope {
        AuthScope::Intent => (
            state.config.intent_hmac_key.as_ref(),
            state.config.intent_caller_id.as_deref(),
        ),
        AuthScope::Exit | AuthScope::Recovery => (
            state.config.exit_hmac_key.as_ref(),
            state.config.exit_caller_id.as_deref(),
        ),
        AuthScope::VenueEvent => (
            state.config.venue_hmac_key.as_ref(),
            state.config.venue_caller_id.as_deref(),
        ),
        AuthScope::AccountSnapshot => (
            state.config.account_hmac_key.as_ref(),
            state.config.account_caller_id.as_deref(),
        ),
        AuthScope::MarketQuote => (
            state.config.market_hmac_key.as_ref(),
            state.config.market_caller_id.as_deref(),
        ),
        AuthScope::AccountCommand => (
            state.config.control_hmac_key.as_ref(),
            state.config.control_caller_id.as_deref(),
        ),
    };
    let (Some(key), Some(caller)) = (key, caller) else {
        return Err((StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled"));
    };
    let timestamp = headers
        .get("X-RTC-Timestamp")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse::<i64>().ok())
        .ok_or((StatusCode::UNAUTHORIZED, "unauthorized"))?;
    let now = SystemTime::now()
        .duration_since(SystemTime::UNIX_EPOCH)
        .map_err(|_| (StatusCode::SERVICE_UNAVAILABLE, "clock unavailable"))?
        .as_secs();
    let now =
        i64::try_from(now).map_err(|_| (StatusCode::SERVICE_UNAVAILABLE, "clock unavailable"))?;
    if timestamp < now.saturating_sub(30) || timestamp > now.saturating_add(30) {
        return Err((StatusCode::UNAUTHORIZED, "unauthorized"));
    }
    let nonce = headers
        .get("X-RTC-Nonce")
        .and_then(|value| value.to_str().ok())
        .filter(|value| valid_nonce(value))
        .ok_or((StatusCode::UNAUTHORIZED, "unauthorized"))?;
    let supplied_caller = headers
        .get("X-RTC-Caller")
        .and_then(|value| value.to_str().ok())
        .ok_or((StatusCode::UNAUTHORIZED, "unauthorized"))?;
    let timestamp_text = timestamp.to_string();
    let signature = headers
        .get("X-RTC-Signature")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| hex::decode(value).ok())
        .ok_or((StatusCode::UNAUTHORIZED, "unauthorized"))?;
    if supplied_caller != caller
        || !verify_request_signature(key, path, caller, &timestamp_text, nonce, body, &signature)
    {
        return Err((StatusCode::UNAUTHORIZED, "unauthorized"));
    }
    store
        .claim_api_nonce(scope.name(), nonce, now.saturating_add(60))
        .await
        .map_err(|error| match error {
            StoreError::AuthorizationReplay => (StatusCode::UNAUTHORIZED, "unauthorized"),
            _ => (StatusCode::SERVICE_UNAVAILABLE, "authorization unavailable"),
        })?;
    Ok(())
}

async fn signer_ready(client: &reqwest::Client, base_url: Option<&str>) -> bool {
    let Some(base_url) = base_url else {
        return false;
    };
    client
        .get(format!("{}/readyz", base_url.trim_end_matches('/')))
        .send()
        .await
        .is_ok_and(|response| response.status().is_success())
}

fn verify_request_signature(
    key: &[u8; 32],
    path: &str,
    caller: &str,
    timestamp: &str,
    nonce: &str,
    body: &[u8],
    signature: &[u8],
) -> bool {
    if signature.len() != 32 {
        return false;
    }
    let digest = Sha256::digest(body);
    let canonical = format!(
        "POST\n{path}\n{caller}\n{timestamp}\n{nonce}\n{}",
        hex::encode(digest)
    );
    let mut mac = Hmac::<Sha256>::new_from_slice(key).expect("fixed-length HMAC key");
    mac.update(canonical.as_bytes());
    mac.verify_slice(signature).is_ok()
}

fn valid_nonce(value: &str) -> bool {
    (32..=128).contains(&value.len())
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_'))
}

fn current_time_ms() -> Result<u64, ()> {
    let millis = SystemTime::now()
        .duration_since(SystemTime::UNIX_EPOCH)
        .map_err(|_| ())?
        .as_millis();
    u64::try_from(millis).map_err(|_| ())
}

fn error(status: StatusCode, message: &str) -> axum::response::Response {
    error_response(status, message)
}

fn error_response(status: StatusCode, message: &str) -> axum::response::Response {
    (status, Json(serde_json::json!({ "error": message }))).into_response()
}

fn store_error_response(error: StoreError) -> axum::response::Response {
    let status = match error {
        StoreError::Database(_) => StatusCode::SERVICE_UNAVAILABLE,
        StoreError::InvalidAction | StoreError::InvalidIntent(_) => StatusCode::BAD_REQUEST,
        StoreError::CoordinatorHalted => StatusCode::SERVICE_UNAVAILABLE,
        StoreError::AccountCommandBlocked => StatusCode::CONFLICT,
        _ => StatusCode::CONFLICT,
    };
    error_response(status, &error.to_string())
}

#[derive(Serialize)]
struct Status {
    status: &'static str,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn request_signature_binds_body() {
        let key = [9; 32];
        let path = "/v1/intents";
        let caller = "shadow-processor";
        let timestamp = "1800000000";
        let nonce = "nnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnn";
        let body = br#"{"id":"one"}"#;
        let digest = Sha256::digest(body);
        let canonical = format!(
            "POST\n{path}\n{caller}\n{timestamp}\n{nonce}\n{}",
            hex::encode(digest)
        );
        let mut mac = Hmac::<Sha256>::new_from_slice(&key).unwrap();
        mac.update(canonical.as_bytes());
        let signature = mac.finalize().into_bytes();

        assert!(verify_request_signature(
            &key, path, caller, timestamp, nonce, body, &signature
        ));
        assert!(!verify_request_signature(
            &key,
            path,
            caller,
            timestamp,
            nonce,
            br#"{"id":"two"}"#,
            &signature,
        ));
    }

    #[test]
    fn authorization_nonce_format_is_bounded() {
        assert!(valid_nonce("nnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnn"));
        assert!(!valid_nonce("short"));
        assert!(!valid_nonce(&"n".repeat(129)));
        assert!(!valid_nonce("nnnnnnnnnnnnnnnnnnnnnnnnnnnnnnn!"));
    }
}
