use crate::product::{IdentitySnapshot, VerifiedWallet};
use anyhow::{anyhow, Result};
use chrono::{TimeZone, Utc};
use reqwest::StatusCode;
use serde::Deserialize;
use serde_json::Value;
use std::time::Duration;

#[derive(Clone)]
pub struct PrivyClient {
    client: reqwest::Client,
    app_id: Option<String>,
    app_secret: Option<String>,
}

#[derive(Deserialize)]
struct PrivyUser {
    id: String,
    #[serde(default)]
    linked_accounts: Vec<Value>,
}

impl PrivyClient {
    pub fn new(app_id: Option<String>, app_secret: Option<String>) -> Self {
        let client = reqwest::Client::builder()
            .timeout(Duration::from_secs(10))
            .build()
            .unwrap_or_else(|_| reqwest::Client::new());
        Self {
            client,
            app_id,
            app_secret,
        }
    }

    pub fn is_enabled(&self) -> bool {
        self.app_id.is_some() && self.app_secret.is_some()
    }

    pub async fn identity(&self, did: &str) -> Result<IdentitySnapshot> {
        let app_id = self
            .app_id
            .as_deref()
            .ok_or_else(|| anyhow!("Privy is not configured"))?;
        let app_secret = self
            .app_secret
            .as_deref()
            .ok_or_else(|| anyhow!("Privy is not configured"))?;

        let response = self
            .client
            .get(format!("https://api.privy.io/v1/users/{did}"))
            .basic_auth(app_id, Some(app_secret))
            .header("privy-app-id", app_id)
            .send()
            .await?;
        if response.status() == StatusCode::NOT_FOUND {
            return Err(anyhow!("Privy user not found"));
        }
        if !response.status().is_success() {
            return Err(anyhow!("Privy returned {}", response.status()));
        }

        let user: PrivyUser = response.json().await?;
        if user.id != did {
            return Err(anyhow!("Privy user identity mismatch"));
        }
        Ok(parse_identity(user.linked_accounts))
    }
}

fn parse_identity(accounts: Vec<Value>) -> IdentitySnapshot {
    let mut wallets = Vec::new();
    let mut embedded_wallet = None;
    let mut has_recovery = false;

    for account in accounts {
        let kind = account
            .get("type")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_ascii_lowercase();
        if kind == "email" || kind == "passkey" {
            has_recovery = true;
        }

        let Some(address) = account.get("address").and_then(Value::as_str) else {
            continue;
        };
        let Ok(address) = crate::product_store::normalize_address(address) else {
            continue;
        };
        let chain_type = account
            .get("chain_type")
            .and_then(Value::as_str)
            .unwrap_or("ethereum");
        if chain_type != "ethereum" {
            continue;
        }

        let client_type = account
            .get("wallet_client_type")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_ascii_lowercase();
        let wallet_type = if kind.contains("smart") {
            "smart"
        } else if kind.contains("embedded") || client_type == "privy" {
            "embedded"
        } else if kind.contains("wallet") || kind.contains("ethereum") {
            "external"
        } else {
            continue;
        };

        let verified_at = account
            .get("latest_verified_at")
            .or_else(|| account.get("verified_at"))
            .and_then(Value::as_i64)
            .and_then(|timestamp| Utc.timestamp_opt(timestamp, 0).single())
            .unwrap_or_else(Utc::now);
        if wallet_type == "embedded" && embedded_wallet.is_none() {
            embedded_wallet = Some(address.clone());
        }
        wallets.push(VerifiedWallet {
            address,
            wallet_type: wallet_type.to_string(),
            verified_at,
        });
    }

    IdentitySnapshot {
        wallets,
        embedded_wallet,
        has_recovery,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn extracts_recovery_and_wallet_types() {
        let snapshot = parse_identity(vec![
            json!({"type": "passkey"}),
            json!({
                "type": "wallet",
                "chain_type": "ethereum",
                "wallet_client_type": "privy",
                "address": "0x1111111111111111111111111111111111111111",
                "latest_verified_at": 1_700_000_000
            }),
            json!({
                "type": "wallet",
                "chain_type": "ethereum",
                "wallet_client_type": "metamask",
                "address": "0x2222222222222222222222222222222222222222"
            }),
        ]);
        assert!(snapshot.has_recovery);
        assert_eq!(snapshot.wallets.len(), 2);
        assert_eq!(snapshot.wallets[0].wallet_type, "embedded");
        assert_eq!(snapshot.wallets[1].wallet_type, "external");
        assert_eq!(
            snapshot.embedded_wallet.as_deref(),
            Some("0x1111111111111111111111111111111111111111")
        );
    }
}
