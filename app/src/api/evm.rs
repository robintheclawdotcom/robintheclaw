use crate::state::AppState;
use actix_web::{web, HttpResponse};
use serde::Deserialize;
use serde_json::json;

pub async fn evm_status(state: web::Data<AppState>) -> HttpResponse {
    HttpResponse::Ok().json(json!({
        "lastSyncedBlock": state.evm_indexer.last_synced_block().await,
        "watched": state.config.watched_addresses(),
    }))
}

#[derive(Deserialize)]
pub struct LogQuery {
    pub limit: Option<usize>,
}

pub async fn evm_logs(state: web::Data<AppState>, q: web::Query<LogQuery>) -> HttpResponse {
    let limit = q.limit.unwrap_or(50).min(500);
    let logs = state.evm_indexer.recent_logs(limit).await;
    let out: Vec<_> = logs
        .into_iter()
        .map(|l| {
            json!({
                "topic0": l.topic0,
                "address": l.log.address,
                "blockNumber": l.log.block_number,
                "txHash": l.log.transaction_hash,
                "logIndex": l.log.log_index,
            })
        })
        .collect();
    HttpResponse::Ok().json(out)
}
