use anyhow::Context;
use robin_runtime::storage::{MarketStatsFeature, Store};
use robin_runtime::{chain::ChainFeed, lighter::LighterFeed, MarketEventKind};
use serde::Deserialize;
use std::{collections::HashSet, env, fs, time::Duration};
use tokio::{
    time::{interval, sleep, MissedTickBehavior},
    try_join,
};
use tracing::{error, info, warn};

#[derive(Deserialize)]
struct Config {
    chain: Chain,
    perp: Perp,
    universe: Vec<String>,
}

#[derive(Deserialize)]
struct Chain {
    mainnet: Mainnet,
}

#[derive(Deserialize)]
struct Mainnet {
    rpc: String,
    #[serde(rename = "sequencerFeed")]
    sequencer_feed: String,
}

#[derive(Deserialize)]
struct Perp {
    api: String,
    websocket: String,
}

#[derive(Deserialize)]
struct UniswapV4 {
    #[serde(rename = "PoolManager")]
    pool_manager: String,
}

#[derive(Deserialize)]
struct ConfigWithUniswap {
    #[serde(flatten)]
    config: Config,
    #[serde(rename = "uniswapV4")]
    uniswap_v4: UniswapV4,
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    robin_runtime::install_tls_provider()?;
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .with_target(false)
        .init();
    let config_path =
        env::var("RUNTIME_CONFIG").unwrap_or_else(|_| "../config/addresses.json".to_string());
    let config: ConfigWithUniswap =
        serde_json::from_slice(&fs::read(&config_path).context("read runtime config")?)
            .context("parse runtime config")?;
    let universe = select_universe(
        &config.config.universe,
        env::var("RUNTIME_UNIVERSE").ok().as_deref(),
    )?;
    let store = Store::from_env().await?;
    let feed = LighterFeed::new(
        config.config.perp.websocket,
        config.config.perp.api,
        universe,
    );
    let sequencer_feed = config.config.chain.mainnet.sequencer_feed;
    let rpc_url = env::var("ROBINHOOD_RPC_URL")
        .ok()
        .filter(|value| !value.trim().is_empty())
        .unwrap_or(config.config.chain.mainnet.rpc);
    let chain = ChainFeed::new(
        rpc_url,
        config.uniswap_v4.pool_manager,
        Duration::from_millis(500),
    );
    info!(
        sequencer_feed,
        "collector starting with live execution disabled"
    );

    let lighter_loop = async {
        loop {
            let result = feed
                .run(|event| {
                    let store = store.clone();
                    async move {
                        let symbol = event.symbol.clone();
                        let event_id = event.id;
                        let received_at = event.received_at;
                        let payload = event.payload.clone();
                        let kind = event.kind;
                        let inserted = store.persist_event(&event).await?;
                        if inserted {
                            store
                                .update_source_health("lighter", "healthy", Some(received_at), None)
                                .await?;
                            if kind == MarketEventKind::Ticker {
                                let bid = payload["ticker"]["b"]["price"]
                                    .as_str()
                                    .and_then(|v| v.parse().ok());
                                let ask = payload["ticker"]["a"]["price"]
                                    .as_str()
                                    .and_then(|v| v.parse().ok());
                                if let Some(symbol) = symbol.as_deref() {
                                    store
                                        .record_feature(
                                            event_id,
                                            symbol,
                                            received_at,
                                            bid,
                                            ask,
                                            Some(0),
                                        )
                                        .await?;
                                }
                            }
                            if kind == MarketEventKind::MarketStats {
                                let stats = &payload["market_stats"];
                                if let Some(symbol) = symbol.as_deref() {
                                    store
                                        .record_market_stats(
                                            event_id,
                                            symbol,
                                            MarketStatsFeature {
                                                observed_at: received_at,
                                                mark: number(stats, "mark_price"),
                                                index: number(stats, "index_price"),
                                                funding_rate: number(stats, "funding_rate"),
                                                open_interest: number(stats, "open_interest"),
                                            },
                                        )
                                        .await?;
                                }
                            }
                        }
                        Ok(())
                    }
                })
                .await;
            match result {
                Ok(()) => warn!("Lighter collector exited; reconnecting"),
                Err(error) => {
                    error!(%error, "Lighter collector failed; reconnecting");
                    store
                        .update_source_health("lighter", "degraded", None, Some(&error.to_string()))
                        .await?;
                }
            }
            sleep(Duration::from_secs(2)).await;
        }
        #[allow(unreachable_code)]
        Ok::<(), anyhow::Error>(())
    };
    let chain_loop = async {
        loop {
            let result = chain
                .run(|event| {
                    let store = store.clone();
                    async move {
                        let source = event.source.clone();
                        let received_at = event.received_at;
                        if store.persist_event(&event).await? {
                            store
                                .update_source_health(&source, "healthy", Some(received_at), None)
                                .await?;
                        }
                        Ok(())
                    }
                })
                .await;
            match result {
                Ok(()) => warn!("Robinhood Chain collector exited; reconnecting"),
                Err(error) => {
                    error!(%error, "Robinhood Chain collector failed; reconnecting");
                    store
                        .update_source_health(
                            "robinhood_chain",
                            "degraded",
                            None,
                            Some(&error.to_string()),
                        )
                        .await?;
                }
            }
            sleep(Duration::from_secs(2)).await;
        }
        #[allow(unreachable_code)]
        Ok::<(), anyhow::Error>(())
    };
    let archive_loop = async {
        let mut maintenance = interval(Duration::from_secs(60 * 60));
        maintenance.set_missed_tick_behavior(MissedTickBehavior::Skip);
        maintenance.tick().await;
        loop {
            tokio::select! {
                _ = maintenance.tick() => {
                    let day = chrono::Utc::now().date_naive() - chrono::Days::new(1);
                    if let Err(error) = store.publish_daily_manifest(day).await {
                        error!(%error, %day, "daily archive manifest failed");
                    }
                    if let Err(error) = store.purge_archived_staging().await {
                        error!(%error, "archive staging cleanup failed");
                    }
                }
                result = store.archive_pending(10_000) => match result {
                    Ok(Some(receipt)) => info!(
                        object_key = receipt.object_key,
                        events = receipt.event_count,
                        "archived raw event segment"
                    ),
                    Ok(None) => sleep(Duration::from_secs(2)).await,
                    Err(error) => {
                        error!(%error, "raw event archival failed");
                        sleep(Duration::from_secs(5)).await;
                    }
                }
            }
        }
        #[allow(unreachable_code)]
        Ok::<(), anyhow::Error>(())
    };
    try_join!(lighter_loop, chain_loop, archive_loop)?;
    Ok(())
}

fn select_universe(canonical: &[String], requested: Option<&str>) -> anyhow::Result<Vec<String>> {
    let Some(requested) = requested else {
        return Ok(canonical.to_vec());
    };
    let allowed = canonical.iter().map(String::as_str).collect::<HashSet<_>>();
    let mut selected = Vec::new();
    let mut seen = HashSet::new();
    for symbol in requested.split(',').map(str::trim) {
        anyhow::ensure!(
            !symbol.is_empty(),
            "RUNTIME_UNIVERSE contains an empty symbol"
        );
        anyhow::ensure!(
            allowed.contains(symbol),
            "RUNTIME_UNIVERSE contains unknown symbol {symbol}"
        );
        anyhow::ensure!(
            seen.insert(symbol),
            "RUNTIME_UNIVERSE contains duplicate symbol {symbol}"
        );
        selected.push(symbol.to_string());
    }
    anyhow::ensure!(!selected.is_empty(), "RUNTIME_UNIVERSE is empty");
    Ok(selected)
}

fn number(value: &serde_json::Value, key: &str) -> Option<f64> {
    value[key].as_str().and_then(|value| value.parse().ok())
}

#[cfg(test)]
mod tests {
    use super::select_universe;

    fn canonical() -> Vec<String> {
        vec!["AAPL".to_string(), "TSLA".to_string()]
    }

    #[test]
    fn universe_override_is_a_nonempty_subset() {
        assert_eq!(
            select_universe(&canonical(), Some("AAPL")).unwrap(),
            ["AAPL"]
        );
        assert!(select_universe(&canonical(), Some("")).is_err());
        assert!(select_universe(&canonical(), Some("AAPL,AAPL")).is_err());
        assert!(select_universe(&canonical(), Some("NVDA")).is_err());
    }
}
