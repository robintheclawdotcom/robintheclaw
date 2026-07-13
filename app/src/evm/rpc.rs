//! JSON-RPC client for Robinhood Chain. Read calls fail over across a deduplicated list of
//! endpoints on rate-limit or transport errors; a broadcast (send-raw) never fails over, so a
//! transaction is not submitted twice.

use anyhow::{anyhow, Result};
use log::warn;
use serde::Deserialize;
use std::time::Duration;

#[derive(Clone)]
pub struct EvmRpc {
    client: reqwest::Client,
    primary_url: String,
    read_urls: Vec<String>,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RpcLog {
    pub address: Option<String>,
    pub transaction_hash: Option<String>,
    pub block_number: Option<String>,
    pub log_index: Option<String>,
    #[serde(default)]
    pub topics: Vec<String>,
    #[serde(default)]
    pub data: String,
}

#[derive(Debug, Deserialize)]
struct JsonRpcResponse {
    result: Option<serde_json::Value>,
    error: Option<JsonRpcError>,
}

#[derive(Debug, Deserialize)]
struct JsonRpcError {
    message: String,
}

impl EvmRpc {
    pub fn new(primary_url: &str, fallback_urls: &[String]) -> Self {
        let mut read_urls = vec![primary_url.trim().to_string()];
        for candidate in fallback_urls {
            let candidate = candidate.trim();
            if candidate.is_empty()
                || read_urls
                    .iter()
                    .any(|existing| existing.eq_ignore_ascii_case(candidate))
            {
                continue;
            }
            read_urls.push(candidate.to_string());
        }

        Self {
            client: reqwest::Client::builder()
                .timeout(Duration::from_secs(10))
                .build()
                .unwrap_or_else(|_| reqwest::Client::new()),
            primary_url: read_urls
                .first()
                .cloned()
                .unwrap_or_else(|| primary_url.trim().to_string()),
            read_urls,
        }
    }

    pub async fn eth_block_number(&self) -> Result<u64> {
        let value = self
            .call("eth_blockNumber", serde_json::json!([]), true)
            .await?;
        parse_u64_hex(
            value
                .as_str()
                .ok_or_else(|| anyhow!("invalid eth_blockNumber response"))?,
        )
    }

    pub async fn eth_call(&self, to: &str, data: &str) -> Result<String> {
        let params = serde_json::json!([{ "to": to, "data": data }, "latest"]);
        let value = self.call("eth_call", params, true).await?;
        value
            .as_str()
            .map(|v| v.to_string())
            .ok_or_else(|| anyhow!("invalid eth_call response"))
    }

    /// Logs for `address`, optionally filtered to a single `topic0`.
    pub async fn eth_get_logs(
        &self,
        address: &str,
        topic0: Option<&str>,
        from_block: u64,
        to_block: u64,
    ) -> Result<Vec<RpcLog>> {
        let mut filter = serde_json::json!({
            "address": address,
            "fromBlock": quantity_hex(from_block),
            "toBlock": quantity_hex(to_block),
        });
        if let Some(topic) = topic0 {
            filter["topics"] = serde_json::json!([topic]);
        }
        let value = self
            .call("eth_getLogs", serde_json::json!([filter]), true)
            .await?;
        serde_json::from_value(value).map_err(|e| anyhow!("invalid eth_getLogs payload: {e}"))
    }

    pub async fn eth_send_raw_transaction(&self, raw_tx: &str) -> Result<String> {
        let value = self
            .call("eth_sendRawTransaction", serde_json::json!([raw_tx]), false)
            .await?;
        value
            .as_str()
            .map(|v| v.to_string())
            .ok_or_else(|| anyhow!("invalid eth_sendRawTransaction response"))
    }

    async fn call(
        &self,
        method: &str,
        params: serde_json::Value,
        allow_failover: bool,
    ) -> Result<serde_json::Value> {
        let endpoints: Vec<&str> = if allow_failover {
            self.read_urls.iter().map(String::as_str).collect()
        } else {
            vec![self.primary_url.as_str()]
        };

        let mut last_err = None;
        for (index, url) in endpoints.iter().enumerate() {
            match self.call_url(url, method, params.clone()).await {
                Ok(value) => return Ok(value),
                Err(err) => {
                    let retry = allow_failover
                        && index + 1 < endpoints.len()
                        && is_retryable(err.to_string().as_str());
                    if retry {
                        warn!("chain RPC {method} failed on endpoint {}: {err}", index + 1);
                        last_err = Some(err);
                        continue;
                    }
                    return Err(err);
                }
            }
        }
        Err(last_err.unwrap_or_else(|| anyhow!("chain RPC response missing result")))
    }

    async fn call_url(
        &self,
        url: &str,
        method: &str,
        params: serde_json::Value,
    ) -> Result<serde_json::Value> {
        let body =
            serde_json::json!({ "jsonrpc": "2.0", "id": 1, "method": method, "params": params });
        let response = self
            .client
            .post(url)
            .json(&body)
            .send()
            .await
            .map_err(|e| anyhow!("chain RPC request failed: {e}"))?;

        if !response.status().is_success() {
            return Err(anyhow!(
                "chain RPC returned non-success status: {}",
                response.status()
            ));
        }

        let payload: JsonRpcResponse = response
            .json()
            .await
            .map_err(|e| anyhow!("failed to decode chain RPC response: {e}"))?;
        if let Some(error) = payload.error {
            return Err(anyhow!("chain RPC error: {}", error.message));
        }
        payload
            .result
            .ok_or_else(|| anyhow!("chain RPC response missing result"))
    }
}

fn is_retryable(message: &str) -> bool {
    let m = message.trim().to_ascii_lowercase();
    m.contains("429")
        || m.contains("403")
        || m.contains("401")
        || m.contains("too many requests")
        || m.contains("forbidden")
        || m.contains("unauthorized")
        || m.contains("timeout")
        || m.contains("connection")
        || m.contains("bad gateway")
        || m.contains("service unavailable")
        || m.contains("temporarily unavailable")
}

pub fn quantity_hex(value: u64) -> String {
    format!("0x{value:x}")
}

pub fn parse_u64_hex(value: &str) -> Result<u64> {
    let trimmed = value.trim_start_matches("0x");
    if trimmed.is_empty() {
        return Err(anyhow!("invalid RPC hex value"));
    }
    let normalized = trimmed.trim_start_matches('0');
    if normalized.is_empty() {
        return Ok(0);
    }
    if normalized.len() > 16 {
        return Err(anyhow!("RPC value out of range for u64"));
    }
    u64::from_str_radix(normalized, 16).map_err(|_| anyhow!("invalid RPC hex value"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn dedups_fallback_endpoints() {
        let rpc = EvmRpc::new(
            "https://primary.example",
            &[
                "https://backup-a.example".into(),
                "https://primary.example".into(),
                "https://backup-b.example".into(),
            ],
        );
        assert_eq!(
            rpc.read_urls,
            vec![
                "https://primary.example".to_string(),
                "https://backup-a.example".to_string(),
                "https://backup-b.example".to_string(),
            ]
        );
    }

    #[test]
    fn retryable_covers_rate_limits_not_reverts() {
        assert!(is_retryable("429 Too Many Requests"));
        assert!(is_retryable("chain RPC request failed: connection reset"));
        assert!(!is_retryable("chain RPC error: execution reverted"));
    }

    #[test]
    fn hex_parsing() {
        assert_eq!(parse_u64_hex("0x0").unwrap(), 0);
        assert_eq!(parse_u64_hex("0x10").unwrap(), 16);
        assert_eq!(quantity_hex(255), "0xff");
    }
}
