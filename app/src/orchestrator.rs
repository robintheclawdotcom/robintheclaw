//! Background service lifecycle. Spawns the reorg-safe indexer loop (persisting its cursor to the
//! store) and the event-bus to live-feed bridge. The basis-scanner service lands in a later phase;
//! its stub is noted below.

use crate::state::AppState;
use log::{info, warn};
use serde_json::json;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::broadcast;

const CURSOR_KEY: &str = "evm_indexer_main";

fn backoff_seconds(streak: u32) -> u64 {
    match streak {
        0 => 20,
        1 => 60,
        2 => 120,
        3 => 240,
        _ => 300,
    }
}

fn is_rate_limited(err: &anyhow::Error) -> bool {
    let m = err.to_string().to_ascii_lowercase();
    m.contains("429") || m.contains("too many requests")
}

fn spawn_indexer(state: &Arc<AppState>) {
    let config = &state.config;
    if !config.evm_enabled {
        info!("evm indexer disabled by config");
        return;
    }
    let addresses = config.watched_addresses();
    if addresses.is_empty() {
        warn!("evm indexer: no watched contract addresses configured; not starting");
        return;
    }

    let indexer = state.evm_indexer.clone();
    let rpc = state.evm_rpc.clone();
    let store = state.store.clone();
    let lookback = config.indexer_lookback_blocks;
    let confirmations = config.indexer_confirmations;

    tokio::spawn(async move {
        if let Some(cursor) = store.get_chain_cursor(CURSOR_KEY).await {
            indexer.set_last_synced_block(cursor.last_block).await;
            info!("restored indexer cursor at block {}", cursor.last_block);
        }
        info!(
            "starting evm indexer loop over {} contract(s)",
            addresses.len()
        );

        let mut streak = 0u32;
        loop {
            let latest = match rpc.eth_block_number().await {
                Ok(block) => block,
                Err(err) => {
                    let delay = if is_rate_limited(&err) {
                        streak = streak.saturating_add(1);
                        warn!("indexer latest-block fetch rate-limited; backing off (streak={streak})");
                        backoff_seconds(streak)
                    } else {
                        streak = 0;
                        warn!("indexer latest-block fetch failed: {err}");
                        backoff_seconds(0)
                    };
                    tokio::time::sleep(Duration::from_secs(delay)).await;
                    continue;
                }
            };
            let target = latest.saturating_sub(confirmations);

            match indexer.sync(&addresses, &[], lookback, Some(target)).await {
                Ok(count) => {
                    streak = 0;
                    let last = indexer.last_synced_block().await;
                    let meta = json!({
                        "latestBlock": latest,
                        "targetBlock": target,
                        "logsIndexed": count,
                        "updatedAt": chrono::Utc::now().to_rfc3339(),
                    });
                    store.upsert_chain_cursor(CURSOR_KEY, last, meta).await;
                }
                Err(err) => {
                    if is_rate_limited(&err) {
                        streak = streak.saturating_add(1);
                        let delay = backoff_seconds(streak);
                        warn!("indexer sync rate-limited; backing off {delay}s (streak={streak})");
                        tokio::time::sleep(Duration::from_secs(delay)).await;
                        continue;
                    }
                    streak = 0;
                    warn!("indexer sync failed: {err}");
                }
            }

            tokio::time::sleep(Duration::from_secs(backoff_seconds(0))).await;
        }
    });
}

fn spawn_event_bridge(state: &Arc<AppState>) {
    let ws_hub = state.ws_hub.clone();
    let mut rx = state.event_bus.subscribe();
    tokio::spawn(async move {
        loop {
            match rx.recv().await {
                Ok(event) => {
                    if let Ok(json) = serde_json::to_string(&event) {
                        ws_hub.broadcast(json);
                    }
                }
                Err(broadcast::error::RecvError::Lagged(n)) => {
                    warn!("event to live-feed bridge lagged by {n} events");
                }
                Err(broadcast::error::RecvError::Closed) => break,
            }
        }
    });
}

// A later phase adds the basis-scanner service: a tokio loop that reads the Uniswap v4 spot and
// the Lighter perp for the tradable universe, evaluates each through the engine, records the
// observation in the store, and emits on the event bus. Its shape mirrors spawn_indexer.
pub fn spawn_background_services(state: Arc<AppState>) {
    spawn_indexer(&state);
    spawn_event_bridge(&state);
}
