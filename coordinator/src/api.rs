use crate::{config::Config, AppState};
use axum::{
    extract::{Path, State},
    http::{header, HeaderMap, StatusCode},
    response::IntoResponse,
    routing::{get, post},
    Json, Router,
};
use execution::{ExecutionEvent, PairIntent};
use serde::Serialize;
use std::sync::Arc;

pub fn routes(state: Arc<AppState>) -> Router {
    Router::new()
        .route("/livez", get(livez))
        .route("/readyz", get(readyz))
        .route("/v1/intents", post(create_intent))
        .route("/v1/intents/{id}/events", post(apply_event))
        .with_state(state)
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
    Json(intent): Json<PairIntent>,
) -> impl IntoResponse {
    if let Err((status, message)) = authorize(&state.config, &headers) {
        return error(status, message);
    }
    let Some(store) = &state.store else {
        return error(StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled");
    };
    match store.create_intent(&intent).await {
        Ok(saga) => (StatusCode::CREATED, Json(serde_json::json!(saga))).into_response(),
        Err(error) => error_response(StatusCode::CONFLICT, &error.to_string()),
    }
}

async fn apply_event(
    State(state): State<Arc<AppState>>,
    Path(intent_id): Path<String>,
    headers: HeaderMap,
    Json(event): Json<ExecutionEvent>,
) -> impl IntoResponse {
    if let Err((status, message)) = authorize(&state.config, &headers) {
        return error(status, message);
    }
    let Some(store) = &state.store else {
        return error(StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled");
    };
    match store.apply_event(&intent_id, event).await {
        Ok(saga) => (StatusCode::OK, Json(serde_json::json!(saga))).into_response(),
        Err(error) => error_response(StatusCode::CONFLICT, &error.to_string()),
    }
}

fn authorize(config: &Config, headers: &HeaderMap) -> Result<(), (StatusCode, &'static str)> {
    if !config.enabled {
        return Err((StatusCode::SERVICE_UNAVAILABLE, "coordinator disabled"));
    }
    let expected = config.api_token.as_deref().unwrap_or_default();
    let provided = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.strip_prefix("Bearer "))
        .unwrap_or_default();
    if !constant_time_eq(expected.as_bytes(), provided.as_bytes()) {
        return Err((StatusCode::UNAUTHORIZED, "unauthorized"));
    }
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

fn constant_time_eq(expected: &[u8], provided: &[u8]) -> bool {
    if expected.len() != provided.len() {
        return false;
    }
    expected
        .iter()
        .zip(provided)
        .fold(0u8, |difference, (left, right)| difference | (left ^ right))
        == 0
}

fn error(status: StatusCode, message: &str) -> axum::response::Response {
    error_response(status, message)
}

fn error_response(status: StatusCode, message: &str) -> axum::response::Response {
    (status, Json(serde_json::json!({ "error": message }))).into_response()
}

#[derive(Serialize)]
struct Status {
    status: &'static str,
}
