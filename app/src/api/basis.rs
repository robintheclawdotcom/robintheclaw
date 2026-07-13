use crate::state::AppState;
use actix_web::{web, HttpResponse};
use serde::Deserialize;

#[derive(Deserialize)]
pub struct Query {
    pub limit: Option<usize>,
}

/// Recent basis observations, newest first. Populated by the basis-scanner service (later phase);
/// empty until then.
pub async fn recent_basis(state: web::Data<AppState>, q: web::Query<Query>) -> HttpResponse {
    let limit = q.limit.unwrap_or(50).min(500);
    HttpResponse::Ok().json(state.store.recent_observations(limit).await)
}
