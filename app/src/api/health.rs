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

pub async fn ready(state: web::Data<AppState>) -> HttpResponse {
    let (chain_head, product_chain_head, database_ready) = tokio::join!(
        state.evm_rpc.eth_block_number(),
        state.product_rpc.eth_block_number(),
        state.product_store.ready()
    );
    let ready = state.config.mainnet_product_ready()
        && chain_head.is_ok_and(|head| head > 0)
        && product_chain_head.is_ok_and(|head| head > 0)
        && database_ready
        && state.auth.is_enabled()
        && state.privy.is_enabled()
        && state.lighter_provisioner.is_enabled()
        && state.robinhood_provisioner.is_enabled()
        && state.readiness_auth.is_enabled()
        && state.coordinator_registration.is_enabled()
        && state.command_dispatcher_ready;
    if ready {
        HttpResponse::Ok().json(json!({ "status": "ready" }))
    } else {
        HttpResponse::ServiceUnavailable().json(json!({ "status": "unready" }))
    }
}
