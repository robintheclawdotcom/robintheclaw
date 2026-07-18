use actix_web::dev::Service as _;
use actix_web::http::header::{HeaderName, HeaderValue};
use actix_web::{middleware, web, App, HttpServer};
use log::info;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Instant;

use app::account_registration::{self, CoordinatorRegistrationClient};
use app::api::configure_routes;
use app::auth::AuthService;
use app::command_dispatcher::{self, CoordinatorCommandClient};
use app::config::Config;
use app::event_bus::EventBus;
use app::evm::{EvmIndexer, EvmRpc};
use app::lighter_provisioner::LighterProvisioner;
use app::orchestrator;
use app::privy::PrivyClient;
use app::product_store::ProductStore;
use app::robinhood_provisioner::RobinhoodProvisioner;
use app::service_auth::ServiceAuth;
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
    let product_rpc = EvmRpc::new(&config.app_rpc_url, &[]);
    let wallet_rpc = EvmRpc::new(&config.alchemy_wallet_rpc_url, &[]);
    let product_store = if let Some(database_url) = config.database_url.as_deref() {
        ProductStore::connect(database_url).await.map_err(|error| {
            std::io::Error::other(format!(
                "application database initialization failed: {error}"
            ))
        })?
    } else {
        log::warn!("DATABASE_URL is not set; authenticated application routes are disabled");
        ProductStore::disabled()
    };
    let auth = AuthService::new(
        config.privy_app_id.clone(),
        config.privy_verification_key.clone(),
    );
    let privy = PrivyClient::new(config.privy_app_id.clone(), config.privy_app_secret.clone());
    let lighter_provisioner = LighterProvisioner::new(
        &config.lighter_provisioner_url,
        &config.lighter_provisioner_caller_id,
        &config.lighter_provisioner_hmac_key,
    )
    .map_err(|error| {
        std::io::Error::other(format!("Lighter provisioner configuration failed: {error}"))
    })?;
    let readiness_auth = ServiceAuth::new(&config.readiness_caller_id, &config.readiness_hmac_key)
        .map_err(|error| {
            std::io::Error::other(format!("readiness publisher configuration failed: {error}"))
        })?;
    let robinhood_provisioner = RobinhoodProvisioner::new(
        &config.robinhood_provisioner_url,
        &config.robinhood_provisioner_caller_id,
        &config.robinhood_provisioner_hmac_key,
    )
    .map_err(|error| {
        std::io::Error::other(format!(
            "Robinhood provisioner configuration failed: {error}"
        ))
    })?;
    let command_client = CoordinatorCommandClient::new(
        &config.coordinator_command_url,
        &config.coordinator_command_caller_id,
        &config.coordinator_command_hmac_key,
    )
    .map_err(|error| {
        std::io::Error::other(format!("coordinator command configuration failed: {error}"))
    })?;
    if !config.coordinator_command_hmac_key.is_empty()
        && config
            .coordinator_command_hmac_key
            .eq_ignore_ascii_case(&config.coordinator_registration_hmac_key)
    {
        return Err(std::io::Error::other(
            "coordinator command and registration HMAC keys must be distinct",
        ));
    }
    let registration_client = CoordinatorRegistrationClient::new(
        &config.coordinator_registration_url,
        &config.coordinator_registration_caller_id,
        &config.coordinator_registration_hmac_key,
    )
    .map_err(|error| {
        std::io::Error::other(format!(
            "coordinator registration configuration failed: {error}"
        ))
    })?;
    let command_dispatcher_ready = command_client.is_enabled();

    let state = Arc::new(AppState {
        config: config.clone(),
        store: Store::new(),
        evm_rpc,
        product_rpc,
        wallet_rpc,
        evm_indexer,
        event_bus: Arc::new(EventBus::new()),
        ws_hub: Arc::new(WsHub::new()),
        product_store,
        auth,
        privy,
        lighter_provisioner,
        robinhood_provisioner,
        readiness_auth,
        coordinator_registration: registration_client.clone(),
        command_dispatcher_ready,
    });

    orchestrator::spawn_background_services(state.clone());
    command_dispatcher::spawn(
        state.product_store.clone(),
        command_client,
        state.lighter_provisioner.clone(),
        config.command_worker_id.clone(),
    );
    account_registration::spawn(
        state.product_store.clone(),
        registration_client,
        config.registration_worker_id.clone(),
    );

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
                let started = Instant::now();
                let path = req.path().to_string();
                let id = format!(
                    "req-{:016x}",
                    REQUEST_COUNTER.fetch_add(1, Ordering::Relaxed)
                );
                let fut = srv.call(req);
                async move {
                    let mut res = fut.await?;
                    log::info!(
                        target: "api_latency",
                        "path={path} status={} duration_ms={}",
                        res.status().as_u16(),
                        started.elapsed().as_millis()
                    );
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
