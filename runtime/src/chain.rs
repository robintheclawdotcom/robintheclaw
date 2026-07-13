use crate::{CanonicalState, Finality, MarketEventKind, RawMarketEvent, SourceIdentity};
use anyhow::Context;
use serde_json::{json, Value};
use std::time::Duration;
use tokio::time::sleep;

const CONNECTOR_VERSION: &str = env!("CARGO_PKG_VERSION");

pub struct ChainFeed {
    client: reqwest::Client,
    rpc_url: String,
    pool_manager: String,
    poll_interval: Duration,
}

impl ChainFeed {
    pub fn new(rpc_url: String, pool_manager: String, poll_interval: Duration) -> Self {
        Self {
            client: reqwest::Client::builder()
                .connect_timeout(Duration::from_secs(5))
                .timeout(Duration::from_secs(10))
                .build()
                .expect("build Robinhood Chain HTTP client"),
            rpc_url,
            pool_manager,
            poll_interval,
        }
    }

    async fn call(&self, method: &str, params: Value) -> anyhow::Result<Value> {
        let response: Value = self
            .client
            .post(&self.rpc_url)
            .json(&json!({ "jsonrpc": "2.0", "id": 1, "method": method, "params": params }))
            .send()
            .await
            .context("request Robinhood Chain RPC")?
            .error_for_status()
            .context("Robinhood Chain RPC response")?
            .json()
            .await
            .context("decode Robinhood Chain RPC response")?;
        if let Some(error) = response.get("error") {
            anyhow::bail!("Robinhood Chain RPC error: {error}");
        }
        response
            .get("result")
            .cloned()
            .context("Robinhood Chain RPC response has no result")
    }

    pub async fn run<F, Fut>(&self, mut handle: F) -> anyhow::Result<()>
    where
        F: FnMut(RawMarketEvent) -> Fut,
        Fut: std::future::Future<Output = anyhow::Result<()>>,
    {
        let mut last_block = None;
        loop {
            let head = self.call("eth_blockNumber", json!([])).await?;
            let head = parse_hex_u64(head.as_str().context("RPC block number is not a string")?)?;
            if last_block.is_none_or(|previous| head > previous) {
                for number in (last_block.map(|previous| previous + 1).unwrap_or(head))..=head {
                    let tag = format!("0x{number:x}");
                    let block = self
                        .call("eth_getBlockByNumber", json!([tag, false]))
                        .await?;
                    let gas_price = self.call("eth_gasPrice", json!([])).await?;
                    let hash = block["hash"]
                        .as_str()
                        .context("chain block has no hash")?
                        .to_string();
                    let parent_hash = block["parentHash"]
                        .as_str()
                        .context("chain block has no parent hash")?
                        .to_string();
                    let raw = serde_json::to_vec(&json!({
                        "block": block,
                        "gas_price": gas_price,
                    }))?;
                    let mut event = RawMarketEvent::from_source(
                        "robinhood_chain",
                        CONNECTOR_VERSION,
                        SourceIdentity::new(
                            "canonical",
                            format!("block:{hash}"),
                            Some(number.to_string()),
                        )?,
                        MarketEventKind::ChainBlock,
                        raw,
                    )?;
                    event.block_number = Some(number as i64);
                    event.block_hash = Some(hash.clone());
                    event.parent_block_hash = Some(parent_hash);
                    event.canonical_state = CanonicalState::Canonical;
                    event.finality = Finality::Confirmed;
                    handle(event).await?;

                    let logs = self
                        .call(
                            "eth_getLogs",
                            json!([{
                                "address": self.pool_manager,
                                "fromBlock": format!("0x{number:x}"),
                                "toBlock": format!("0x{number:x}"),
                            }]),
                        )
                        .await?;
                    for log in logs
                        .as_array()
                        .context("eth_getLogs result is not an array")?
                    {
                        let raw = serde_json::to_vec(log)?;
                        let log_index = log["logIndex"]
                            .as_str()
                            .context("chain log has no log index")?;
                        let transaction_hash = log["transactionHash"]
                            .as_str()
                            .context("chain log has no transaction hash")?;
                        let mut event = RawMarketEvent::from_source(
                            "robinhood_chain",
                            CONNECTOR_VERSION,
                            SourceIdentity::new(
                                "canonical",
                                format!("log:{hash}:{transaction_hash}:{log_index}"),
                                Some(format!("{number}:{log_index}")),
                            )?,
                            MarketEventKind::PoolState,
                            raw,
                        )?;
                        event.block_number = Some(number as i64);
                        event.block_hash = Some(hash.clone());
                        event.canonical_state = CanonicalState::Canonical;
                        event.finality = Finality::Confirmed;
                        handle(event).await?;
                    }
                }
                last_block = Some(head);
            }
            sleep(self.poll_interval).await;
        }
    }
}

fn parse_hex_u64(value: &str) -> anyhow::Result<u64> {
    u64::from_str_radix(value.trim_start_matches("0x"), 16).context("parse hex quantity")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_rpc_quantities() {
        assert_eq!(parse_hex_u64("0x123").unwrap(), 291);
    }
}
