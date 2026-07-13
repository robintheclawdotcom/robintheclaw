use actix_web::web;

pub mod basis;
pub mod error;
pub mod evm;
pub mod health;
pub mod product;

pub fn configure_routes(cfg: &mut web::ServiceConfig) {
    cfg.route("/health", web::get().to(health::health)).service(
        web::scope("/api")
            .route("/health", web::get().to(health::health))
            .route("/basis", web::get().to(basis::recent_basis))
            .route("/evm/status", web::get().to(evm::evm_status))
            .route("/evm/logs", web::get().to(evm::evm_logs)),
    );
    cfg.route("/ws", web::get().to(crate::ws::ws_index));
    cfg.service(
        web::scope("/api/v1")
            .route("/me", web::get().to(product::me))
            .route("/me/wallets/sync", web::post().to(product::sync_wallets))
            .route(
                "/me/preferences",
                web::put().to(product::update_preferences),
            )
            .route("/dashboard", web::get().to(product::dashboard))
            .route("/activity", web::get().to(product::activity))
            .route("/metrics", web::post().to(product::metric))
            .route("/vaults/prepare", web::post().to(product::prepare_vault))
            .route("/vaults/confirm", web::post().to(product::confirm_vault))
            .route("/ws", web::get().to(crate::ws::product_ws_index)),
    );
}
