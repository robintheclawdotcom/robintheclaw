use crate::state::AppState;
use actix_web::{web, HttpResponse};
use serde::Deserialize;

const DEFAULT_LIMIT: i64 = 50;
const MAX_LIMIT: i64 = 500;

#[derive(Deserialize)]
pub struct LimitQuery {
    limit: Option<i64>,
}

#[derive(Deserialize)]
pub struct CaptureQuery {
    hours: Option<i64>,
}

pub async fn source_health(state: web::Data<AppState>) -> HttpResponse {
    respond(&state, state.store.source_health().await)
}

pub async fn capture_summary(
    state: web::Data<AppState>,
    query: web::Query<CaptureQuery>,
) -> HttpResponse {
    let hours = query.hours.unwrap_or(24).clamp(1, 168);
    respond(&state, state.store.capture_summary(hours).await)
}

pub async fn shadow_intents(
    state: web::Data<AppState>,
    query: web::Query<LimitQuery>,
) -> HttpResponse {
    let limit = query.limit.unwrap_or(DEFAULT_LIMIT).clamp(1, MAX_LIMIT);
    respond(&state, state.store.recent_shadow_intents(limit).await)
}

pub async fn datasets(state: web::Data<AppState>, query: web::Query<LimitQuery>) -> HttpResponse {
    let limit = query.limit.unwrap_or(DEFAULT_LIMIT).clamp(1, MAX_LIMIT);
    respond(&state, state.store.dataset_snapshots(limit).await)
}

pub async fn metrics(state: web::Data<AppState>) -> HttpResponse {
    HttpResponse::Ok()
        .content_type("text/plain; version=0.0.4; charset=utf-8")
        .body(state.metrics.encode())
}

fn respond<T: serde::Serialize>(state: &AppState, result: Result<T, sqlx::Error>) -> HttpResponse {
    match result {
        Ok(value) => HttpResponse::Ok().json(value),
        Err(error) => {
            state.metrics.record_database_failure();
            log::error!("control-plane database query failed: {error}");
            HttpResponse::ServiceUnavailable()
                .json(serde_json::json!({ "error": "database_unavailable" }))
        }
    }
}
