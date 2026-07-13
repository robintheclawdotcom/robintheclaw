use coordinator::{api, config::Config, AppState};
use std::sync::Arc;
use tokio::net::TcpListener;
use tracing_subscriber::{layer::SubscriberExt, util::SubscriberInitExt};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    tracing_subscriber::registry()
        .with(
            tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into()),
        )
        .with(tracing_subscriber::fmt::layer().json())
        .init();
    let config = Config::from_env()?;
    let listen = config.listen;
    let state = Arc::new(AppState::initialize(config).await?);
    let listener = TcpListener::bind(listen).await?;
    tracing::info!(%listen, enabled = state.config.enabled, "coordinator listening");
    axum::serve(listener, api::routes(state))
        .with_graceful_shutdown(shutdown())
        .await?;
    Ok(())
}

async fn shutdown() {
    let _ = tokio::signal::ctrl_c().await;
}
