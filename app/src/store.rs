//! Application state store. In-memory today; a Postgres-backed implementation swaps in behind
//! these same methods once durable persistence and cross-restart cursors are needed, with no
//! change at the call sites.

use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::HashMap;
use std::sync::Arc;
use tokio::sync::RwLock;

const MAX_OBSERVATIONS: usize = 10_000;

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct ChainCursor {
    pub last_block: u64,
    pub meta: Value,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct BasisRecord {
    pub symbol: String,
    pub spot_price: f64,
    pub perp_mark: f64,
    pub basis_bps: f64,
    pub liquidity: f64,
    pub observed_at: i64,
}

#[derive(Default)]
struct Inner {
    cursors: HashMap<String, ChainCursor>,
    observations: Vec<BasisRecord>,
}

#[derive(Clone, Default)]
pub struct Store {
    inner: Arc<RwLock<Inner>>,
}

impl Store {
    pub fn new() -> Self {
        Self::default()
    }

    pub async fn get_chain_cursor(&self, key: &str) -> Option<ChainCursor> {
        self.inner.read().await.cursors.get(key).cloned()
    }

    pub async fn upsert_chain_cursor(&self, key: &str, last_block: u64, meta: Value) {
        self.inner
            .write()
            .await
            .cursors
            .insert(key.to_string(), ChainCursor { last_block, meta });
    }

    pub async fn record_observation(&self, rec: BasisRecord) {
        let mut g = self.inner.write().await;
        g.observations.push(rec);
        let len = g.observations.len();
        if len > MAX_OBSERVATIONS {
            g.observations.drain(0..len - MAX_OBSERVATIONS);
        }
    }

    /// Most recent observations, newest first.
    pub async fn recent_observations(&self, limit: usize) -> Vec<BasisRecord> {
        let g = self.inner.read().await;
        g.observations.iter().rev().take(limit).cloned().collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[tokio::test]
    async fn cursor_upsert_and_get() {
        let store = Store::new();
        assert!(store.get_chain_cursor("main").await.is_none());
        store
            .upsert_chain_cursor("main", 100, json!({"k": 1}))
            .await;
        store
            .upsert_chain_cursor("main", 200, json!({"k": 2}))
            .await;
        assert_eq!(
            store.get_chain_cursor("main").await.unwrap().last_block,
            200
        );
    }

    #[tokio::test]
    async fn observations_are_newest_first() {
        let store = Store::new();
        for i in 0..3 {
            store
                .record_observation(BasisRecord {
                    symbol: format!("S{i}"),
                    spot_price: 1.0,
                    perp_mark: 1.0,
                    basis_bps: 0.0,
                    liquidity: 1.0,
                    observed_at: i,
                })
                .await;
        }
        let recent = store.recent_observations(2).await;
        assert_eq!(recent.len(), 2);
        assert_eq!(recent[0].symbol, "S2");
    }
}
