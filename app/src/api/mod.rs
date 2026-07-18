use actix_web::web;

pub mod basis;
pub mod error;
pub mod evm;
pub mod health;
pub mod internal;
pub mod product;

pub fn configure_routes(cfg: &mut web::ServiceConfig) {
    cfg.route("/health", web::get().to(health::health))
        .route("/readyz", web::get().to(health::ready))
        .service(
            web::scope("/internal/v1")
                .app_data(web::PayloadConfig::new(64 << 10))
                .route("/readiness", web::post().to(internal::record_readiness)),
        )
        .service(
            web::scope("/api")
                .route("/health", web::get().to(health::health))
                .route("/basis", web::get().to(basis::recent_basis))
                .route("/evm/status", web::get().to(evm::evm_status))
                .route("/evm/logs", web::get().to(evm::evm_logs))
                .service(
                    web::scope("/v1")
                        .route("/me", web::get().to(product::me))
                        .route("/me/wallets/sync", web::post().to(product::sync_wallets))
                        .route(
                            "/me/preferences",
                            web::put().to(product::update_preferences),
                        )
                        .route("/dashboard", web::get().to(product::dashboard))
                        .route("/agents", web::post().to(product::launch_agent))
                        .route("/agents/{id}", web::put().to(product::update_agent_status))
                        .route(
                            "/agents/{id}/execution-account",
                            web::post().to(product::create_execution_account),
                        )
                        .route(
                            "/agents/{id}/lighter/link-request",
                            web::post().to(product::lighter_link_request),
                        )
                        .route(
                            "/agents/{id}/lighter/confirm",
                            web::post().to(product::lighter_confirm),
                        )
                        .route(
                            "/agents/{id}/lighter/revocation",
                            web::get().to(product::lighter_revocation),
                        )
                        .route(
                            "/agents/{id}/lighter/revocation/confirm",
                            web::post().to(product::lighter_revocation_confirm),
                        )
                        .route(
                            "/agents/{id}/robinhood/prepare",
                            web::post().to(product::robinhood_prepare),
                        )
                        .route(
                            "/agents/{id}/robinhood/confirm",
                            web::post().to(product::robinhood_confirm),
                        )
                        .route(
                            "/agents/{id}/readiness",
                            web::get().to(product::agent_readiness),
                        )
                        .route(
                            "/agents/{id}/execution",
                            web::get().to(product::agent_execution),
                        )
                        .route(
                            "/agents/{id}/commands",
                            web::post().to(product::create_agent_command),
                        )
                        .route(
                            "/agents/{id}/commands/pending",
                            web::get().to(product::pending_agent_command),
                        )
                        .route(
                            "/agents/{id}/commands/{command_id}",
                            web::get().to(product::agent_command),
                        )
                        .route("/activity", web::get().to(product::activity))
                        .route("/metrics", web::post().to(product::metric))
                        .route("/vaults/prepare", web::post().to(product::prepare_vault))
                        .route("/vaults/confirm", web::post().to(product::confirm_vault))
                        .route("/ws", web::get().to(crate::ws::product_ws_index)),
                ),
        );
    cfg.route("/ws", web::get().to(crate::ws::ws_index));
}

#[cfg(test)]
mod tests {
    use super::*;
    use actix_web::{http::StatusCode, test, App};

    #[actix_web::test]
    async fn product_routes_are_not_shadowed_by_api_scope() {
        let app = test::init_service(App::new().configure(configure_routes)).await;
        let request = test::TestRequest::get().uri("/api/v1/me").to_request();
        let response = test::call_service(&app, request).await;

        assert_ne!(response.status(), StatusCode::NOT_FOUND);
    }
}
