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
    pub api_key_index: u8,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ConfirmLink<'a> {
    pub execution_account_id: Uuid,
    pub link_id: Uuid,
    pub l1_signature: &'a str,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct RevocationRequest {
    pub execution_account_id: Uuid,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ConfirmRevocation<'a> {
    pub execution_account_id: Uuid,
    pub revocation_id: Uuid,
    pub l1_signature: &'a str,
}

#[derive(Clone, Debug)]
pub struct RevocationBinding {
    pub execution_account_id: Uuid,
    pub owner_address: String,
    pub account_index: i64,
    pub api_key_index: u8,
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

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct PublicRevocation {
    pub revocation_id: Uuid,
    pub execution_account_id: Uuid,
    pub owner_address: String,
    pub account_index: i64,
    pub api_key_index: u8,
    pub tombstone_public_key: Option<String>,
    pub status: String,
    pub message_to_sign: Option<String>,
    pub transaction_hash: Option<String>,
    pub registered_public_key: Option<String>,
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
        let client = Client::builder()
            .connect_timeout(Duration::from_secs(3))
            .timeout(Duration::from_secs(20))
            .redirect(reqwest::redirect::Policy::none())
            .build()?;
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

    pub async fn prepare(&self, request: &PrepareLink<'_>) -> Result<PublicLink> {
        self.post("/v1/links/prepare", request).await
    }

    pub async fn confirm(&self, request: &ConfirmLink<'_>) -> Result<PublicLink> {
        self.post("/v1/links/confirm", request).await
    }

    pub async fn prepare_revocation(
        &self,
        binding: &RevocationBinding,
    ) -> Result<PublicRevocation> {
        let result = self
            .post(
                "/v1/links/revoke/prepare",
                &RevocationRequest {
                    execution_account_id: binding.execution_account_id,
                },
            )
            .await?;
        validate_revocation(result, binding)
    }

    pub async fn revocation_status(&self, binding: &RevocationBinding) -> Result<PublicRevocation> {
        let result = self
            .post(
                "/v1/links/revoke/status",
                &RevocationRequest {
                    execution_account_id: binding.execution_account_id,
                },
            )
            .await?;
        validate_revocation(result, binding)
    }

    pub async fn confirm_revocation(
        &self,
        request: &ConfirmRevocation<'_>,
        binding: &RevocationBinding,
    ) -> Result<PublicRevocation> {
        if request.execution_account_id != binding.execution_account_id {
            return Err(anyhow!(
                "Lighter revocation request does not match its binding"
            ));
        }
        let result = self.post("/v1/links/revoke/confirm", request).await?;
        validate_revocation(result, binding)
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
        let mut response = self
            .client
            .post(url)
            .header("Content-Type", "application/json")
            .header("X-RTC-Caller", &self.caller_id)
            .header("X-RTC-Timestamp", timestamp)
            .header("X-RTC-Nonce", &nonce)
            .header("X-RTC-Signature", signature)
            .body(body)
            .send()
            .await?;
        let status = response.status();
        let response_signature = response
            .headers()
            .get("X-RTC-Response-Signature")
            .and_then(|value| value.to_str().ok())
            .map(str::to_owned);
        if response
            .content_length()
            .is_some_and(|length| length > MAX_RESPONSE_BYTES as u64)
        {
            return Err(LighterProvisionerError::Unavailable.into());
        }
        let mut response_body = Vec::new();
        while let Some(chunk) = response.chunk().await? {
            if response_body.len().saturating_add(chunk.len()) > MAX_RESPONSE_BYTES {
                return Err(LighterProvisionerError::Unavailable.into());
            }
            response_body.extend_from_slice(&chunk);
        }
        verify_response_signature(
            hmac_key,
            path,
            &self.caller_id,
            &nonce,
            status,
            &response_body,
            response_signature.as_deref(),
        )?;
        if !status.is_success() {
            let message = serde_json::from_slice::<ServiceError>(&response_body)
                .ok()
                .map(|value| value.error)
                .unwrap_or_else(|| "provisioner request failed".to_string());
            return Err(service_error(status, &message));
        }
        serde_json::from_slice(&response_body)
            .map_err(|_| LighterProvisionerError::Unavailable.into())
    }
}

fn verify_response_signature(
    key: &[u8; 32],
    path: &str,
    caller: &str,
    nonce: &str,
    status: StatusCode,
    body: &[u8],
    signature: Option<&str>,
) -> Result<()> {
    let provided = signature
        .ok_or(LighterProvisionerError::Unavailable)
        .and_then(|value| hex::decode(value).map_err(|_| LighterProvisionerError::Unavailable))?;
    let canonical = format!(
        "RESPONSE\n{path}\n{caller}\n{nonce}\n{}\n{}",
        status.as_u16(),
        hex::encode(Sha256::digest(body)),
    );
    let mut mac =
        HmacSha256::new_from_slice(key).map_err(|_| LighterProvisionerError::Unavailable)?;
    mac.update(canonical.as_bytes());
    mac.verify_slice(&provided)
        .map_err(|_| LighterProvisionerError::Unavailable.into())
}

fn validate_revocation(
    value: PublicRevocation,
    binding: &RevocationBinding,
) -> Result<PublicRevocation> {
    let owner = normalize_address(&value.owner_address);
    let expected_owner = normalize_address(&binding.owner_address);
    let tombstone = value
        .tombstone_public_key
        .as_deref()
        .map(normalize_public_key)
        .filter(|key| key.len() == 80 && key.bytes().all(|byte| byte.is_ascii_hexdigit()));
    if value.execution_account_id != binding.execution_account_id
        || owner.is_none()
        || owner != expected_owner
        || value.account_index != binding.account_index
        || value.api_key_index != binding.api_key_index
        || binding.account_index <= 0
        || !(4..=254).contains(&binding.api_key_index)
        || !matches!(value.status.as_str(), "pending" | "verifying" | "revoked")
        || tombstone.is_none()
    {
        return Err(anyhow!("invalid Lighter revocation response"));
    }
    match value.status.as_str() {
        "pending" if value.message_to_sign.as_deref().is_none_or(str::is_empty) => {
            return Err(anyhow!(
                "Lighter revocation omitted the owner signature payload"
            ));
        }
        "revoked" => {
            let registered = value
                .registered_public_key
                .as_deref()
                .map(normalize_public_key);
            if registered != tombstone
                || value
                    .transaction_hash
                    .as_deref()
                    .is_none_or(|hash| !valid_hash(hash))
                || value.message_to_sign.is_some()
            {
                return Err(anyhow!("Lighter revocation proof is invalid"));
            }
        }
        _ => {}
    }
    Ok(value)
}

fn normalize_public_key(value: &str) -> String {
    value
        .strip_prefix("0x")
        .unwrap_or(value)
        .to_ascii_lowercase()
}

fn normalize_address(value: &str) -> Option<String> {
    let normalized = value.strip_prefix("0x")?;
    if normalized.len() != 40
        || normalized.bytes().any(|byte| !byte.is_ascii_hexdigit())
        || normalized.bytes().all(|byte| byte == b'0')
    {
        return None;
    }
    Some(normalized.to_ascii_lowercase())
}

fn valid_hash(value: &str) -> bool {
    value.len() == 66
        && value.starts_with("0x")
        && value[2..].bytes().all(|byte| byte.is_ascii_hexdigit())
}

#[derive(Deserialize)]
struct ServiceError {
    error: String,
}

#[derive(Debug, thiserror::Error)]
pub enum LighterProvisionerError {
    #[error("{0}")]
    Rejected(String),
    #[error("{0}")]
    Conflict(String),
    #[error("Lighter provisioner is unavailable")]
    Unavailable,
}

fn service_error(status: StatusCode, message: &str) -> anyhow::Error {
    match status {
        StatusCode::CONFLICT => LighterProvisionerError::Conflict(message.to_string()).into(),
        StatusCode::BAD_REQUEST | StatusCode::NOT_FOUND => {
            LighterProvisionerError::Rejected(message.to_string()).into()
        }
        _ => LighterProvisionerError::Unavailable.into(),
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

    #[test]
    fn revoked_response_requires_exact_registered_tombstone() {
        let execution_account_id = Uuid::new_v4();
        let tombstone = "11".repeat(40);
        let binding = RevocationBinding {
            execution_account_id,
            owner_address: "0x1111111111111111111111111111111111111111".into(),
            account_index: 7,
            api_key_index: 4,
        };
        let value = PublicRevocation {
            revocation_id: Uuid::new_v4(),
            execution_account_id,
            owner_address: "0x1111111111111111111111111111111111111111".into(),
            account_index: 7,
            api_key_index: 4,
            tombstone_public_key: Some(format!("0x{tombstone}")),
            status: "revoked".into(),
            message_to_sign: None,
            transaction_hash: Some(format!("0x{}", "22".repeat(32))),
            registered_public_key: Some(tombstone),
            created_at: "2026-01-01T00:00:00Z".into(),
            updated_at: "2026-01-01T00:00:01Z".into(),
        };
        assert!(validate_revocation(value.clone(), &binding).is_ok());

        let mut substituted = value.clone();
        substituted.execution_account_id = Uuid::new_v4();
        assert!(validate_revocation(substituted, &binding).is_err());

        let mut mismatched = value.clone();
        mismatched.registered_public_key = Some("33".repeat(40));
        assert!(validate_revocation(mismatched, &binding).is_err());

        for substituted in [
            PublicRevocation {
                owner_address: "0x2222222222222222222222222222222222222222".into(),
                ..value.clone()
            },
            PublicRevocation {
                account_index: 8,
                ..value.clone()
            },
            PublicRevocation {
                api_key_index: 5,
                ..value
            },
        ] {
            assert!(validate_revocation(substituted, &binding).is_err());
        }
    }

    #[test]
    fn provisioner_response_signature_binds_status_path_nonce_and_body() {
        let key = [7u8; 32];
        let path = "/v1/links/revoke/status";
        let caller = "robin-api";
        let nonce = "0123456789abcdef0123456789abcdef";
        let status = StatusCode::OK;
        let body = br#"{"status":"revoked"}"#;
        let canonical = format!(
            "RESPONSE\n{path}\n{caller}\n{nonce}\n{}\n{}",
            status.as_u16(),
            hex::encode(Sha256::digest(body)),
        );
        let mut mac = HmacSha256::new_from_slice(&key).unwrap();
        mac.update(canonical.as_bytes());
        let signature = hex::encode(mac.finalize().into_bytes());

        assert!(verify_response_signature(
            &key,
            path,
            caller,
            nonce,
            status,
            body,
            Some(&signature),
        )
        .is_ok());
        assert!(verify_response_signature(
            &key,
            path,
            caller,
            nonce,
            StatusCode::ACCEPTED,
            body,
            Some(&signature),
        )
        .is_err());
        assert!(verify_response_signature(
            &key,
            path,
            caller,
            nonce,
            status,
            br#"{"status":"verifying"}"#,
            Some(&signature),
        )
        .is_err());
        assert!(verify_response_signature(&key, path, caller, nonce, status, body, None).is_err());
    }
}
