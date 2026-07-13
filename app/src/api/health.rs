use crate::state::AppState;
use actix_web::{web, HttpResponse};
use serde_json::json;

/// Liveness plus a cheap chain probe: the current head and the indexer's persisted cursor.
pub async fn health(state: web::Data<AppState>) -> HttpResponse {
    let chain_head = state.evm_rpc.eth_block_number().await.ok();
    let cursor = state.store.get_chain_cursor("evm_indexer_main").await;
    HttpResponse::Ok().json(json!({
        "status": "ok",
        "chainId": state.config.chain_id,
        "chainHead": chain_head,
        "indexerCursor": cursor.map(|c| c.last_block),
    }))
}
