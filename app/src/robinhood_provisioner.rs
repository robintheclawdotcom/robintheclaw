use anyhow::{anyhow, Result};
use chrono::Utc;
use hmac::{Hmac, Mac};
use reqwest::{Client, StatusCode, Url};
use serde::{de::DeserializeOwned, Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::time::Duration;
use uuid::Uuid;

type HmacSha256 = Hmac<Sha256>;
const MAX_RESPONSE_BYTES: usize = 64 << 10;

#[derive(Clone)]
pub struct RobinhoodProvisioner {
    client: Client,
    base_url: Option<Url>,
    caller_id: String,
    hmac_key: Option<[u8; 32]>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PrepareGraph<'a> {
    pub execution_account_id: Uuid,
    pub owner_address: &'a str,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ConfirmGraph<'a> {
    pub execution_account_id: Uuid,
    pub deployment_transaction_hash: &'a str,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Graph {
    pub risk_manager: String,
    pub spot_adapter: String,
    pub vault: String,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct UnsignedAction {
    pub kind: String,
    pub chain_id: String,
    pub to: String,
    pub value: String,
    pub data: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct PublicGraphBinding {
    pub execution_account_id: Uuid,
    pub owner_address: String,
    pub signer_address: String,
    pub key_version: i64,
    pub factory_address: String,
    pub registry_address: String,
    pub policy_digest: String,
    pub graph: Graph,
    pub status: String,
    pub deployment_transaction_hash: Option<String>,
    pub deployment_block: Option<u64>,
    #[serde(default)]
    pub actions: Vec<UnsignedAction>,
    pub updated_at: String,
}

impl RobinhoodProvisioner {
    pub fn new(base_url: &str, caller_id: &str, hmac_key_hex: &str) -> Result<Self> {
        if base_url.trim().is_empty() && hmac_key_hex.trim().is_empty() {
            return Ok(Self::disabled());
        }
        if caller_id.len() < 3
            || caller_id.len() > 64
            || !caller_id
                .bytes()
                .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
        {
            return Err(anyhow!("invalid Robinhood provisioner caller id"));
        }
        let normalized = if base_url.starts_with("http://") || base_url.starts_with("https://") {
            base_url.to_string()
        } else {
            format!("http://{base_url}")
        };
        let mut url =
            Url::parse(&normalized).map_err(|_| anyhow!("invalid Robinhood provisioner URL"))?;
        if url.host_str().is_none()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.query().is_some()
            || url.fragment().is_some()
        {
            return Err(anyhow!("invalid Robinhood provisioner URL"));
        }
        url.set_path("");
        let key = hex::decode(hmac_key_hex)
            .map_err(|_| anyhow!("invalid Robinhood provisioner HMAC key"))?;
        let hmac_key: [u8; 32] = key
            .try_into()
            .map_err(|_| anyhow!("invalid Robinhood provisioner HMAC key"))?;
        let client = Client::builder()
            .connect_timeout(Duration::from_secs(3))
            .timeout(Duration::from_secs(10))
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|_| anyhow!("initialize Robinhood provisioner client"))?;
        Ok(Self {
            client,
            base_url: Some(url),
            caller_id: caller_id.to_string(),
            hmac_key: Some(hmac_key),
        })
    }

    pub fn disabled() -> Self {
        Self {
            client: Client::new(),
            base_url: None,
            caller_id: String::new(),
            hmac_key: None,
        }
    }

    pub fn is_enabled(&self) -> bool {
        self.base_url.is_some() && self.hmac_key.is_some()
    }

    pub async fn prepare(&self, request: &PrepareGraph<'_>) -> Result<PublicGraphBinding> {
        self.post("/v1/graphs/prepare", request).await
    }

    pub async fn confirm(&self, request: &ConfirmGraph<'_>) -> Result<PublicGraphBinding> {
        self.post("/v1/graphs/confirm", request).await
    }

    async fn post<T: Serialize, R: DeserializeOwned>(&self, path: &str, request: &T) -> Result<R> {
        let base_url = self
            .base_url
            .as_ref()
            .ok_or_else(|| anyhow!("Robinhood provisioner is disabled"))?;
        let hmac_key = self
            .hmac_key
            .as_ref()
            .ok_or_else(|| anyhow!("Robinhood provisioner is disabled"))?;
        let body = serde_json::to_vec(request)?;
        let timestamp = Utc::now().timestamp().to_string();
        let nonce = Uuid::new_v4().simple().to_string();
        let digest = Sha256::digest(&body);
        let canonical = format!(
            "POST\n{path}\n{}\n{timestamp}\n{nonce}\n{}",
            self.caller_id,
            hex::encode(digest)
        );
        let mut mac = HmacSha256::new_from_slice(hmac_key)
            .map_err(|_| anyhow!("invalid Robinhood provisioner HMAC key"))?;
        mac.update(canonical.as_bytes());
        let signature = hex::encode(mac.finalize().into_bytes());
        let mut response = self
            .client
            .post(base_url.join(path)?)
            .header("Content-Type", "application/json")
            .header("X-RTC-Caller", &self.caller_id)
            .header("X-RTC-Timestamp", timestamp)
            .header("X-RTC-Nonce", nonce)
            .header("X-RTC-Signature", signature)
            .body(body)
            .send()
            .await
            .map_err(|_| anyhow!("Robinhood provisioner is unavailable"))?;
        let status = response.status();
        if response
            .content_length()
            .is_some_and(|length| length > MAX_RESPONSE_BYTES as u64)
        {
            return Err(anyhow!("Robinhood provisioner response is too large"));
        }
        let mut response_body = Vec::new();
        while let Some(chunk) = response
            .chunk()
            .await
            .map_err(|_| anyhow!("Robinhood provisioner is unavailable"))?
        {
            if response_body.len() + chunk.len() > MAX_RESPONSE_BYTES {
                return Err(anyhow!("Robinhood provisioner response is too large"));
            }
            response_body.extend_from_slice(&chunk);
        }
        if !status.is_success() {
            let message = serde_json::from_slice::<ServiceError>(&response_body)
                .ok()
                .map(|value| value.error)
                .unwrap_or_else(|| "provisioner request failed".to_string());
            return Err(service_error(status, &message));
        }
        serde_json::from_slice(&response_body)
            .map_err(|_| anyhow!("Robinhood provisioner returned an invalid response"))
    }
}

#[derive(Deserialize)]
struct ServiceError {
    error: String,
}

fn service_error(status: StatusCode, message: &str) -> anyhow::Error {
    match status {
        StatusCode::CONFLICT | StatusCode::BAD_REQUEST | StatusCode::NOT_FOUND => {
            anyhow!("Robinhood graph rejected: {message}")
        }
        _ => anyhow!("Robinhood provisioner is unavailable"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn disabled_without_configuration() {
        assert!(!RobinhoodProvisioner::new("", "robin-api", "")
            .unwrap()
            .is_enabled());
    }

    #[test]
    fn rejects_partial_or_invalid_configuration() {
        assert!(RobinhoodProvisioner::new("internal:8080", "robin-api", "00").is_err());
        assert!(RobinhoodProvisioner::new("internal:8080", "Robin API", &"00".repeat(32)).is_err());
    }

    #[test]
    fn rejects_private_fields_in_public_response() {
        let body = format!(
            r#"{{
                "executionAccountId":"{}",
                "ownerAddress":"0x1111111111111111111111111111111111111111",
                "kmsKeyId":"private-key-reference",
                "signerAddress":"0x2222222222222222222222222222222222222222",
                "keyVersion":1,
                "factoryAddress":"0x3333333333333333333333333333333333333333",
                "registryAddress":"0x4444444444444444444444444444444444444444",
                "policyDigest":"0x{}",
                "graph":{{
                    "riskManager":"0x5555555555555555555555555555555555555555",
                    "spotAdapter":"0x6666666666666666666666666666666666666666",
                    "vault":"0x7777777777777777777777777777777777777777"
                }},
                "status":"awaiting_deployment",
                "actions":[],
                "updatedAt":"2026-01-01T00:00:00Z"
            }}"#,
            Uuid::new_v4(),
            "11".repeat(32)
        );
        assert!(serde_json::from_str::<PublicGraphBinding>(&body).is_err());
    }
}
