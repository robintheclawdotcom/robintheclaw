use crate::state::AppState;
use actix_web::{web, HttpResponse};
use serde::Serialize;

#[derive(Serialize)]
struct Health<'a> {
    status: &'a str,
}

pub async fn live() -> HttpResponse {
    HttpResponse::Ok().json(Health { status: "live" })
}

pub async fn ready(state: web::Data<AppState>) -> HttpResponse {
    match state.store.ready().await {
        Ok(true) => HttpResponse::Ok().json(Health { status: "ready" }),
        Ok(false) => HttpResponse::ServiceUnavailable().json(Health {
            status: "schema_incomplete",
        }),
        Err(error) => {
            state.metrics.record_database_failure();
            log::error!("control-plane readiness query failed: {error}");
            HttpResponse::ServiceUnavailable().json(Health {
                status: "database_unavailable",
            })
        }
    }
}
