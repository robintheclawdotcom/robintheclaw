use actix_web::dev::Service as _;
use actix_web::http::header::{HeaderName, HeaderValue};
use actix_web::{middleware, web, App, HttpServer};
use log::info;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use app::api::configure_routes;
use app::config::Config;
use app::event_bus::EventBus;
use app::evm::{EvmIndexer, EvmRpc};
use app::orchestrator;
use app::state::AppState;
use app::store::Store;
use app::ws::WsHub;

static REQUEST_COUNTER: AtomicU64 = AtomicU64::new(0);

#[actix_web::main]
async fn main() -> std::io::Result<()> {
    if std::env::var("RUST_LOG").is_err() {
        std::env::set_var("RUST_LOG", "info");
    }
    env_logger::init();

    let config = Config::from_env();
    let bind_addr = format!("{}:{}", config.host, config.port);

    let evm_rpc = EvmRpc::new(&config.rpc_url, &config.rpc_fallback_urls);
    let evm_indexer = EvmIndexer::new(evm_rpc.clone(), config.indexer_max_logs);

    let state = Arc::new(AppState {
        config: config.clone(),
        store: Store::new(),
        evm_rpc,
        evm_indexer,
        event_bus: Arc::new(EventBus::new()),
        ws_hub: Arc::new(WsHub::new()),
    });

    orchestrator::spawn_background_services(state.clone());

    info!(
        "starting http server on {bind_addr} (chain id {})",
        config.chain_id
    );
    let data = web::Data::from(state);

    HttpServer::new(move || {
        App::new()
            .app_data(data.clone())
            .wrap(middleware::Logger::default())
            .wrap(middleware::Compress::default())
            .wrap(
                middleware::DefaultHeaders::new()
                    .add(("X-Content-Type-Options", "nosniff"))
                    .add(("X-Frame-Options", "DENY"))
                    .add(("Referrer-Policy", "strict-origin-when-cross-origin")),
            )
            .wrap_fn(|req, srv| {
                let id = format!(
                    "req-{:016x}",
                    REQUEST_COUNTER.fetch_add(1, Ordering::Relaxed)
                );
                let fut = srv.call(req);
                async move {
                    let mut res = fut.await?;
                    if let Ok(value) = HeaderValue::from_str(&id) {
                        res.headers_mut()
                            .insert(HeaderName::from_static("x-request-id"), value);
                    }
                    Ok(res)
                }
            })
            .app_data(web::JsonConfig::default().limit(4 * 1024 * 1024))
            .configure(configure_routes)
    })
    .bind(&bind_addr)?
    .run()
    .await
}
