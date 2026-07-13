use crate::evm::abi;
use crate::evm::rpc::{parse_u64_hex, RpcLog};
use crate::product_store::ContractActivity;
use crate::state::AppState;
use anyhow::{anyhow, Result};
use serde_json::json;
use sha3::{Digest, Keccak256};
use std::collections::HashMap;
use std::sync::{Arc, OnceLock};
use std::time::Duration;
use uuid::Uuid;

const CURSOR: &str = "product_activity_v1";

pub fn spawn(state: Arc<AppState>) {
    if !state.product_store.is_enabled() || !state.config.product_contracts_ready() {
        return;
    }
    tokio::spawn(async move {
        loop {
            if let Err(error) = sync(&state).await {
                log::warn!("product activity indexer failed: {error}");
            }
            tokio::time::sleep(Duration::from_secs(15)).await;
        }
    });
}

async fn sync(state: &AppState) -> Result<()> {
    let latest = state.product_rpc.eth_block_number().await?;
    let target = latest.saturating_sub(state.config.indexer_confirmations);
    let from = match state.product_store.activity_cursor(CURSOR).await? {
        Some(block) => block.saturating_add(1),
        None => target.saturating_sub(state.config.indexer_lookback_blocks),
    };
    if from > target {
        return Ok(());
    }

    for log in state
        .product_rpc
        .eth_get_logs(&state.config.personal_vault_factory, None, from, target)
        .await?
    {
        let Some(owner) = log
            .topics
            .get(1)
            .and_then(|topic| abi::decode_address(topic).ok())
        else {
            continue;
        };
        let Some(user_id) = state
            .product_store
            .user_for_smart_account(state.config.app_chain_id, &owner)
            .await?
        else {
            continue;
        };
        persist(state, user_id, log).await?;
    }

    for (user_id, address) in state.product_store.watched_contracts().await? {
        for log in state
            .product_rpc
            .eth_get_logs(&address, None, from, target)
            .await?
        {
            persist(state, user_id, log).await?;
        }
    }

    state
        .product_store
        .set_activity_cursor(CURSOR, target)
        .await
}

async fn persist(state: &AppState, user_id: Uuid, log: RpcLog) -> Result<()> {
    let topic = log
        .topics
        .first()
        .ok_or_else(|| anyhow!("contract log has no topic"))?;
    let Some(kind) = event_kinds().get(&topic.to_ascii_lowercase()) else {
        return Ok(());
    };
    let transaction_hash = log
        .transaction_hash
        .as_deref()
        .ok_or_else(|| anyhow!("contract log has no transaction hash"))?;
    let block_number = parse_u64_hex(
        log.block_number
            .as_deref()
            .ok_or_else(|| anyhow!("contract log has no block number"))?,
    )?;
    let log_index = parse_u64_hex(
        log.log_index
            .as_deref()
            .ok_or_else(|| anyhow!("contract log has no index"))?,
    )?;
    let inserted = state
        .product_store
        .record_contract_activity(&ContractActivity {
            user_id,
            chain_id: state.config.app_chain_id,
            kind: (*kind).to_string(),
            transaction_hash: transaction_hash.to_string(),
            block_number,
            log_index,
            payload: json!({ "contract": log.address, "topics": log.topics, "data": log.data }),
        })
        .await?;
    if inserted {
        state.ws_hub.broadcast(
            json!({
                "type": "activity",
                "kind": kind,
                "transactionHash": transaction_hash,
                "blockNumber": block_number,
            })
            .to_string(),
        );
    }
    Ok(())
}

fn event_kinds() -> &'static HashMap<String, &'static str> {
    static EVENTS: OnceLock<HashMap<String, &'static str>> = OnceLock::new();
    EVENTS.get_or_init(|| {
        HashMap::from([
            (
                topic("VaultCreated(address,address,address,address,address,uint64)"),
                "vault_created",
            ),
            (topic("AgentSet(address)"), "agent_rotated"),
            (topic("Deposited(address,uint256)"), "deposit"),
            (topic("Withdrawn(address,uint256)"), "withdrawal"),
            (topic("Executed(address,bytes4,uint256)"), "execution"),
            (topic("BatchAnchored(bytes32,uint64,uint64)"), "attestation"),
            (topic("ExecutorSet(address)"), "executor_updated"),
            (
                topic("TargetAllowed(address,bytes4,bool)"),
                "target_policy_updated",
            ),
            (topic("CapUpdated(uint256,uint64)"), "mandate_updated"),
            (topic("HaltSet(bool)"), "strategy_state_changed"),
            (
                topic("Checked(address,bytes4,uint256,uint256)"),
                "mandate_checked",
            ),
            (
                topic("RootAnchored(uint64,bytes32,uint64,uint64)"),
                "attestation",
            ),
        ])
    })
}

fn topic(signature: &str) -> String {
    format!("0x{}", hex::encode(Keccak256::digest(signature.as_bytes())))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn recognizes_product_events() {
        assert_eq!(
            event_kinds().get(&topic("HaltSet(bool)")),
            Some(&"strategy_state_changed")
        );
        assert_eq!(topic("Deposited(address,uint256)").len(), 66);
    }
}
