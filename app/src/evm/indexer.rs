//! Reorg-safe log indexer. It only reads up to a confirmations-deep target block, dedups logs by
//! (topic, tx hash, log index), keeps them newest-first, and caps the retained set. Its cursor is
//! persisted by the orchestrator so a restart resumes rather than re-scanning from genesis.

use crate::evm::rpc::{EvmRpc, RpcLog};
use anyhow::Result;
use std::sync::Arc;
use tokio::sync::RwLock;

#[derive(Clone)]
pub struct IndexedLog {
    pub topic0: String,
    pub log: RpcLog,
}

struct State {
    last_synced_block: u64,
    logs: Vec<IndexedLog>,
}

#[derive(Clone)]
pub struct EvmIndexer {
    rpc: EvmRpc,
    state: Arc<RwLock<State>>,
    max_logs: usize,
}

impl EvmIndexer {
    pub fn new(rpc: EvmRpc, max_logs: usize) -> Self {
        Self {
            rpc,
            state: Arc::new(RwLock::new(State {
                last_synced_block: 0,
                logs: Vec::new(),
            })),
            max_logs,
        }
    }

    /// Fetch new logs for the watched addresses up to `latest_override` (a confirmations-deep
    /// target supplied by the caller) and fold them into the retained set. Returns the retained
    /// log count. On the first run it looks back `lookback_blocks`; afterwards it resumes from the
    /// last synced block.
    pub async fn sync(
        &self,
        addresses: &[String],
        topics: &[&str],
        lookback_blocks: u64,
        latest_override: Option<u64>,
    ) -> Result<usize> {
        let latest_block = match latest_override {
            Some(v) => v,
            None => self.rpc.eth_block_number().await?,
        };
        let from_block = {
            let state = self.state.read().await;
            if state.last_synced_block == 0 {
                latest_block.saturating_sub(lookback_blocks)
            } else {
                state.last_synced_block.saturating_add(1)
            }
        };
        if from_block > latest_block {
            return Ok(0);
        }

        let mut additions = Vec::new();
        for address in addresses {
            if topics.is_empty() {
                for log in self
                    .rpc
                    .eth_get_logs(address, None, from_block, latest_block)
                    .await?
                {
                    let topic0 = log.topics.first().cloned().unwrap_or_default();
                    additions.push(IndexedLog { topic0, log });
                }
            } else {
                for topic0 in topics {
                    for log in self
                        .rpc
                        .eth_get_logs(address, Some(topic0), from_block, latest_block)
                        .await?
                    {
                        additions.push(IndexedLog {
                            topic0: topic0.to_string(),
                            log,
                        });
                    }
                }
            }
        }

        let mut state = self.state.write().await;
        merge_logs(&mut state.logs, additions, self.max_logs);
        state.last_synced_block = latest_block;
        Ok(state.logs.len())
    }

    pub async fn set_last_synced_block(&self, block: u64) {
        self.state.write().await.last_synced_block = block;
    }

    pub async fn last_synced_block(&self) -> u64 {
        self.state.read().await.last_synced_block
    }

    pub async fn recent_logs(&self, limit: usize) -> Vec<IndexedLog> {
        self.state
            .read()
            .await
            .logs
            .iter()
            .take(limit)
            .cloned()
            .collect()
    }

    pub async fn logs_by_topic(&self, topic0: &str) -> Vec<RpcLog> {
        self.state
            .read()
            .await
            .logs
            .iter()
            .filter(|item| item.topic0.eq_ignore_ascii_case(topic0))
            .map(|item| item.log.clone())
            .collect()
    }
}

fn log_key(item: &IndexedLog) -> (String, String, String) {
    (
        item.topic0.clone(),
        item.log.transaction_hash.clone().unwrap_or_default(),
        item.log.log_index.clone().unwrap_or_default(),
    )
}

/// Merge new logs into the retained set: drop duplicates, sort newest-first by block then log
/// index, and cap at `max`.
fn merge_logs(existing: &mut Vec<IndexedLog>, additions: Vec<IndexedLog>, max: usize) {
    for item in additions {
        let key = log_key(&item);
        if !existing.iter().any(|e| log_key(e) == key) {
            existing.push(item);
        }
    }
    existing.sort_by(|a, b| {
        let ba = a.log.block_number.clone().unwrap_or_default();
        let bb = b.log.block_number.clone().unwrap_or_default();
        bb.cmp(&ba).then_with(|| {
            let ia = a.log.log_index.clone().unwrap_or_default();
            let ib = b.log.log_index.clone().unwrap_or_default();
            ib.cmp(&ia)
        })
    });
    if existing.len() > max {
        existing.truncate(max);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn mk(topic: &str, tx: &str, idx: &str, block: &str) -> IndexedLog {
        IndexedLog {
            topic0: topic.into(),
            log: RpcLog {
                address: Some("0xabc".into()),
                transaction_hash: Some(tx.into()),
                block_number: Some(block.into()),
                log_index: Some(idx.into()),
                topics: vec![topic.into()],
                data: String::new(),
            },
        }
    }

    #[test]
    fn merge_dedups_and_caps() {
        let mut set = Vec::new();
        merge_logs(&mut set, vec![mk("0x1", "0xtx1", "0x0", "0x10")], 10);
        // duplicate (same topic/tx/idx) is ignored
        merge_logs(&mut set, vec![mk("0x1", "0xtx1", "0x0", "0x10")], 10);
        assert_eq!(set.len(), 1);
        merge_logs(&mut set, vec![mk("0x1", "0xtx2", "0x0", "0x11")], 10);
        assert_eq!(set.len(), 2);
        // newest block first
        assert_eq!(set[0].log.block_number.as_deref(), Some("0x11"));
    }

    #[test]
    fn merge_respects_cap() {
        let mut set = Vec::new();
        for i in 0..5 {
            merge_logs(
                &mut set,
                vec![mk("0x1", &format!("0xtx{i}"), "0x0", &format!("0x{i}"))],
                3,
            );
        }
        assert_eq!(set.len(), 3);
    }
}
