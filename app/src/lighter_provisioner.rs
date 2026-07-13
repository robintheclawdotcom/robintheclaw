use anyhow::{anyhow, Result};
use chrono::Utc;
use hmac::{Hmac, Mac};
use reqwest::{Client, StatusCode, Url};
use serde::{de::DeserializeOwned, Deserialize, Serialize};
use sha2::{Digest, Sha256};
use uuid::Uuid;

type HmacSha256 = Hmac<Sha256>;

#[derive(Clone)]
pub struct LighterProvisioner {
    client: Client,
    base_url: Option<Url>,
    caller_id: String,
    hmac_key: Option<[u8; 32]>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PrepareLink<'a> {
    pub execution_account_id: Uuid,
    pub owner_address: &'a str,
    pub account_index: i64,
    pub api_key_index: u8,
    pub nonce: i64,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ConfirmLink<'a> {
    pub execution_account_id: Uuid,
    pub link_id: Uuid,
    pub l1_signature: &'a str,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PublicLink {
    pub link_id: Uuid,
    pub execution_account_id: Uuid,
    pub owner_address: String,
    pub account_index: i64,
    pub api_key_index: u8,
    pub credential_version: i64,
    pub public_key: Option<String>,
    pub status: String,
    pub message_to_sign: Option<String>,
    pub transaction_hash: Option<String>,
    pub created_at: String,
    pub updated_at: String,
}

impl LighterProvisioner {
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
            return Err(anyhow!("invalid Lighter provisioner caller id"));
        }
        let normalized = if base_url.starts_with("http://") || base_url.starts_with("https://") {
            base_url.to_string()
        } else {
            format!("http://{base_url}")
        };
        let mut url =
            Url::parse(&normalized).map_err(|_| anyhow!("invalid Lighter provisioner URL"))?;
        if url.host_str().is_none() || url.query().is_some() || url.fragment().is_some() {
            return Err(anyhow!("invalid Lighter provisioner URL"));
        }
        url.set_path("");
        let key = hex::decode(hmac_key_hex)
            .map_err(|_| anyhow!("invalid Lighter provisioner HMAC key"))?;
        let hmac_key: [u8; 32] = key
            .try_into()
            .map_err(|_| anyhow!("invalid Lighter provisioner HMAC key"))?;
        Ok(Self {
            client: Client::new(),
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

    pub async fn prepare(&self, request: &PrepareLink<'_>) -> Result<PublicLink> {
        self.post("/v1/links/prepare", request).await
    }

    pub async fn confirm(&self, request: &ConfirmLink<'_>) -> Result<PublicLink> {
        self.post("/v1/links/confirm", request).await
    }

    async fn post<T: Serialize, R: DeserializeOwned>(&self, path: &str, request: &T) -> Result<R> {
        let base_url = self
            .base_url
            .as_ref()
            .ok_or_else(|| anyhow!("Lighter provisioner is disabled"))?;
        let hmac_key = self
            .hmac_key
            .as_ref()
            .ok_or_else(|| anyhow!("Lighter provisioner is disabled"))?;
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
            .map_err(|_| anyhow!("invalid Lighter provisioner HMAC key"))?;
        mac.update(canonical.as_bytes());
        let signature = hex::encode(mac.finalize().into_bytes());
        let url = base_url.join(path)?;
        let response = self
            .client
            .post(url)
            .header("Content-Type", "application/json")
            .header("X-RTC-Caller", &self.caller_id)
            .header("X-RTC-Timestamp", timestamp)
            .header("X-RTC-Nonce", nonce)
            .header("X-RTC-Signature", signature)
            .body(body)
            .send()
            .await?;
        if !response.status().is_success() {
            let status = response.status();
            let message = response
                .json::<ServiceError>()
                .await
                .ok()
                .map(|value| value.error)
                .unwrap_or_else(|| "provisioner request failed".to_string());
            return Err(service_error(status, &message));
        }
        response.json().await.map_err(Into::into)
    }
}

#[derive(Deserialize)]
struct ServiceError {
    error: String,
}

fn service_error(status: StatusCode, message: &str) -> anyhow::Error {
    match status {
        StatusCode::CONFLICT | StatusCode::BAD_REQUEST | StatusCode::NOT_FOUND => {
            anyhow!("Lighter association rejected: {message}")
        }
        _ => anyhow!("Lighter provisioner is unavailable"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn disabled_without_configuration() {
        assert!(!LighterProvisioner::new("", "robin-api", "")
            .unwrap()
            .is_enabled());
    }

    #[test]
    fn rejects_partial_or_invalid_configuration() {
        assert!(LighterProvisioner::new("internal:8080", "robin-api", "00").is_err());
        assert!(LighterProvisioner::new("internal:8080", "Robin API", &"00".repeat(32)).is_err());
    }
}
