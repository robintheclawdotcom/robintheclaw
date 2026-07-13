use actix_web::web;

pub mod basis;
pub mod evm;
pub mod health;

pub fn configure_routes(cfg: &mut web::ServiceConfig) {
    cfg.route("/health", web::get().to(health::health)).service(
        web::scope("/api")
            .route("/health", web::get().to(health::health))
            .route("/basis", web::get().to(basis::recent_basis))
            .route("/evm/status", web::get().to(evm::evm_status))
            .route("/evm/logs", web::get().to(evm::evm_logs)),
    );
    cfg.route("/ws", web::get().to(crate::ws::ws_index));
}
