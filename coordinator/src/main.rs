use coordinator::{api, config::Config, worker::Worker, AppState};
use std::sync::Arc;
use tokio::{net::TcpListener, sync::watch};
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
    let (shutdown_tx, shutdown_rx) = watch::channel(false);
    let worker = if state.config.enabled {
        Some(tokio::spawn(
            Worker::new(
                state.store.clone().expect("validated execution store"),
                state.signers.clone().expect("validated signer clients"),
                state.config.worker_id.clone().expect("validated worker ID"),
            )
            .run(shutdown_rx),
        ))
    } else {
        None
    };
    let listener = TcpListener::bind(listen).await?;
    tracing::info!(%listen, enabled = state.config.enabled, "coordinator listening");
    axum::serve(listener, api::routes(state))
        .with_graceful_shutdown(shutdown(shutdown_tx.clone()))
        .await?;
    let _ = shutdown_tx.send(true);
    if let Some(worker) = worker {
        let _ = worker.await;
    }
    Ok(())
}

async fn shutdown(signal: watch::Sender<bool>) {
    #[cfg(unix)]
    {
        let mut terminate =
            tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                .expect("install SIGTERM handler");
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {}
            _ = terminate.recv() => {}
        }
    }
    #[cfg(not(unix))]
    let _ = tokio::signal::ctrl_c().await;
    let _ = signal.send(true);
}
