use crate::product::ExecutionAccountRecord;
use crate::product_store::ProductStore;
use anyhow::{anyhow, Result};
use chrono::Utc;
use hmac::{Hmac, Mac};
use reqwest::{Client, StatusCode, Url};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::time::Duration;
use thiserror::Error;
use uuid::Uuid;

type HmacSha256 = Hmac<Sha256>;
const REGISTRATION_PATH: &str = "/v1/account-registrations";
const EXECUTION_PATH: &str = "/v1/account-executions";
const MAX_RESPONSE_BYTES: usize = 64 << 10;

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq, sqlx::FromRow)]
pub struct AccountRegistration {
    pub execution_account_id: Uuid,
    pub agent_id: Uuid,
    pub strategy_version: String,
    pub risk_version: String,
    pub strategy_manifest_sha256: String,
    pub lighter_account_index: i64,
    pub lighter_api_key_index: i16,
    pub robinhood_owner: String,
    pub robinhood_vault: String,
    pub robinhood_signer: String,
    pub binding_sha256: String,
}

impl AccountRegistration {
    pub fn calculate_binding_sha256(&self) -> String {
        hex::encode(Sha256::digest(format!(
            "robin.execution-account-binding.v1\0{}\n{}\n{}\n{}\n{}\n{}\n{}\n{}\n{}\n{}",
            self.execution_account_id,
            self.agent_id,
            self.strategy_version,
            self.risk_version,
            self.strategy_manifest_sha256,
            self.lighter_account_index,
            self.lighter_api_key_index,
            self.robinhood_owner,
            self.robinhood_vault,
            self.robinhood_signer,
        )))
    }

    pub fn validate(&self) -> Result<()> {
        if self.strategy_version != "basis-aapl-v1"
            || self.risk_version != "basis-aapl-v1"
            || self.strategy_manifest_sha256
                != "27df8d5a56b45f6966f8a60d866a55cfddfc65835216def5def023126c96c937"
            || self.lighter_account_index <= 0
            || !(4..=254).contains(&self.lighter_api_key_index)
            || !valid_address(&self.robinhood_owner)
            || !valid_address(&self.robinhood_vault)
            || !valid_address(&self.robinhood_signer)
            || self.robinhood_owner == self.robinhood_vault
            || self.robinhood_owner == self.robinhood_signer
            || self.robinhood_vault == self.robinhood_signer
            || self.binding_sha256 != self.calculate_binding_sha256()
        {
            return Err(anyhow!("invalid coordinator account registration"));
        }
        Ok(())
    }
}

#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AccountRegistrationReadiness {
    pub venue_approved: bool,
    pub oracle_healthy: bool,
    pub sequencer_healthy: bool,
    pub reconciliation_ready: bool,
    pub exit_authority_ready: bool,
    pub alerting_ready: bool,
    pub safe_rotation_ready: bool,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AccountRegistrationResponse {
    pub execution_account_id: String,
    pub agent_id: String,
    pub strategy_version: String,
    pub risk_version: String,
    pub strategy_manifest_sha256: String,
    pub lighter_account_index: i64,
    pub lighter_api_key_index: i16,
    pub robinhood_owner: String,
    pub robinhood_vault: String,
    pub robinhood_signer: String,
    pub binding_sha256: String,
    pub account_status: String,
    pub control_mode: String,
    pub readiness: AccountRegistrationReadiness,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AccountExecutionResponse {
    pub schema_version: u8,
    pub execution_account_id: String,
    pub agent_id: String,
    pub strategy_version: String,
    pub strategy_manifest_sha256: String,
    pub account_status: String,
    pub control_mode: String,
    pub active: bool,
    pub flat: bool,
    pub intent_id: Option<String>,
    pub symbol: Option<String>,
    pub state: String,
    pub spot_amount_raw: String,
    pub spot_decimals: u8,
    pub perp_open_base: String,
    pub perp_base_decimals: u8,
    pub spot_notional_micros: String,
    pub perp_notional_micros: String,
    pub lighter_order_id: Option<String>,
    pub lighter_transaction_hash: Option<String>,
    pub robinhood_transaction_hash: Option<String>,
    pub lighter_unwind_order_id: Option<String>,
    pub lighter_unwind_transaction_hash: Option<String>,
    pub robinhood_unwind_transaction_hash: Option<String>,
    pub updated_at_ms: u64,
}

impl AccountExecutionResponse {
    fn matches(&self, account: &ExecutionAccountRecord) -> bool {
        let identity_matches = self.schema_version == 1
            && self.execution_account_id == account.id.to_string()
            && self.agent_id == account.agent_id.to_string()
            && self.strategy_version == account.strategy_version
            && self.strategy_manifest_sha256 == account.strategy_manifest_sha256
            && matches!(
                self.account_status.as_str(),
                "active" | "blocked" | "closed"
            )
            && matches!(
                self.control_mode.as_str(),
                "ACTIVE" | "REDUCE_ONLY" | "HALTED"
            )
            && matches!(
                self.state.as_str(),
                "flat"
                    | "created"
                    | "prechecked"
                    | "perp_submitted"
                    | "perp_partial"
                    | "perp_filled"
                    | "spot_submitted"
                    | "hedged"
                    | "exiting"
                    | "unwinding"
                    | "closed"
                    | "cancelled"
                    | "expired"
                    | "unhedged"
                    | "failed_safe"
            );
        if !identity_matches
            || self.spot_decimals > 18
            || self.perp_base_decimals > 18
            || !bounded_decimal(&self.spot_amount_raw)
            || !bounded_decimal(&self.perp_open_base)
            || !bounded_notional(&self.spot_notional_micros)
            || !bounded_notional(&self.perp_notional_micros)
            || self.lighter_order_id.as_deref().is_some_and(|value| {
                value.is_empty()
                    || value.len() > 128
                    || !value.bytes().all(|byte| byte.is_ascii_graphic())
            })
            || self.lighter_order_id.is_some() != self.lighter_transaction_hash.is_some()
            || self
                .lighter_transaction_hash
                .as_deref()
                .is_some_and(|value| !valid_bytes32(value))
            || self
                .robinhood_transaction_hash
                .as_deref()
                .is_some_and(|value| !valid_bytes32(value))
            || self
                .lighter_unwind_order_id
                .as_deref()
                .is_some_and(|value| {
                    value.is_empty()
                        || value.len() > 128
                        || !value.bytes().all(|byte| byte.is_ascii_graphic())
                })
            || self.lighter_unwind_order_id.is_some()
                != self.lighter_unwind_transaction_hash.is_some()
            || self
                .lighter_unwind_transaction_hash
                .as_deref()
                .is_some_and(|value| !valid_bytes32(value))
            || self
                .robinhood_unwind_transaction_hash
                .as_deref()
                .is_some_and(|value| !valid_bytes32(value))
        {
            return false;
        }
        match (&self.intent_id, &self.symbol) {
            (None, None) => {
                !self.active
                    && self.flat
                    && self.state == "flat"
                    && self.spot_amount_raw == "0"
                    && self.perp_open_base == "0"
                    && self.spot_notional_micros == "0"
                    && self.perp_notional_micros == "0"
                    && self.lighter_order_id.is_none()
                    && self.lighter_transaction_hash.is_none()
                    && self.robinhood_transaction_hash.is_none()
                    && self.lighter_unwind_order_id.is_none()
                    && self.lighter_unwind_transaction_hash.is_none()
                    && self.robinhood_unwind_transaction_hash.is_none()
                    && self.updated_at_ms == 0
            }
            (Some(intent_id), Some(symbol)) => {
                let expected_flat =
                    matches!(self.state.as_str(), "closed" | "cancelled" | "expired");
                let requires_lighter_proof = self.perp_open_base != "0"
                    || matches!(
                        self.state.as_str(),
                        "perp_partial"
                            | "perp_filled"
                            | "spot_submitted"
                            | "hedged"
                            | "exiting"
                            | "unwinding"
                            | "closed"
                            | "unhedged"
                    );
                let requires_robinhood_proof = self.spot_amount_raw != "0"
                    || matches!(self.state.as_str(), "hedged" | "exiting");
                let closed = self.state == "closed";
                valid_bytes32(intent_id)
                    && symbol == "AAPL"
                    && self.state != "flat"
                    && self.flat == expected_flat
                    && self.active != self.flat
                    && (!self.flat || (self.spot_amount_raw == "0" && self.perp_open_base == "0"))
                    && (!requires_lighter_proof || self.lighter_order_id.is_some())
                    && (!requires_robinhood_proof || self.robinhood_transaction_hash.is_some())
                    && (!closed || self.lighter_unwind_order_id.is_some())
                    && (!closed
                        || self.robinhood_transaction_hash.is_none()
                        || self.robinhood_unwind_transaction_hash.is_some())
                    && self.updated_at_ms > 0
            }
            _ => false,
        }
    }
}

impl AccountRegistrationResponse {
    pub(crate) fn matches(&self, registration: &AccountRegistration) -> bool {
        self.execution_account_id == registration.execution_account_id.to_string()
            && self.agent_id == registration.agent_id.to_string()
            && self.strategy_version == registration.strategy_version
            && self.risk_version == registration.risk_version
            && self.strategy_manifest_sha256 == registration.strategy_manifest_sha256
            && self.lighter_account_index == registration.lighter_account_index
            && self.lighter_api_key_index == registration.lighter_api_key_index
            && self.robinhood_owner == registration.robinhood_owner
            && self.robinhood_vault == registration.robinhood_vault
            && self.robinhood_signer == registration.robinhood_signer
            && self.binding_sha256 == registration.binding_sha256
            && matches!(
                self.account_status.as_str(),
                "active" | "blocked" | "closed"
            )
            && matches!(
                self.control_mode.as_str(),
                "ACTIVE" | "REDUCE_ONLY" | "HALTED"
            )
    }
}

#[derive(Clone)]
pub struct CoordinatorRegistrationClient {
    client: Client,
    base_url: Option<Url>,
    caller_id: String,
    hmac_key: Option<[u8; 32]>,
}

#[derive(Debug, Error)]
pub enum RegistrationClientError {
    #[error("coordinator account registration is disabled")]
    Disabled,
    #[error("coordinator account registration request failed")]
    Transport,
    #[error("coordinator rejected account registration with status {0}")]
    Rejected(u16),
    #[error("coordinator account registration conflicts with an existing identity")]
    Conflict,
    #[error("coordinator returned an invalid account registration")]
    InvalidResponse,
}

pub enum RegistrationLookup {
    Found(Box<AccountRegistrationResponse>),
    Missing,
}

impl CoordinatorRegistrationClient {
    pub fn new(base_url: &str, caller_id: &str, hmac_key_hex: &str) -> Result<Self> {
        if base_url.trim().is_empty() && hmac_key_hex.trim().is_empty() {
            return Ok(Self::disabled());
        }
        if base_url.trim().is_empty() || hmac_key_hex.trim().is_empty() {
            return Err(anyhow!("incomplete coordinator registration configuration"));
        }
        if !(3..=64).contains(&caller_id.len())
            || !caller_id
                .bytes()
                .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
        {
            return Err(anyhow!("invalid coordinator registration caller id"));
        }
        let normalized = if base_url.starts_with("http://") || base_url.starts_with("https://") {
            base_url.to_string()
        } else {
            format!("http://{base_url}")
        };
        let mut url =
            Url::parse(&normalized).map_err(|_| anyhow!("invalid coordinator registration URL"))?;
        if url.host_str().is_none()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.query().is_some()
            || url.fragment().is_some()
        {
            return Err(anyhow!("invalid coordinator registration URL"));
        }
        url.set_path("");
        let hmac_key: [u8; 32] = hex::decode(hmac_key_hex)
            .map_err(|_| anyhow!("invalid coordinator registration HMAC key"))?
            .try_into()
            .map_err(|_| anyhow!("invalid coordinator registration HMAC key"))?;
        let client = Client::builder()
            .connect_timeout(Duration::from_secs(3))
            .timeout(Duration::from_secs(10))
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|_| anyhow!("initialize coordinator registration client"))?;
        Ok(Self {
            client,
            base_url: Some(url),
            caller_id: caller_id.into(),
            hmac_key: Some(hmac_key),
        })
    }

    fn disabled() -> Self {
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

    pub async fn lookup(
        &self,
        registration: &AccountRegistration,
    ) -> Result<RegistrationLookup, RegistrationClientError> {
        let path = format!("{REGISTRATION_PATH}/{}", registration.execution_account_id);
        let (status, body) = self.request("GET", &path, Vec::new()).await?;
        if status == StatusCode::NOT_FOUND {
            return Ok(RegistrationLookup::Missing);
        }
        if status == StatusCode::CONFLICT {
            return Err(RegistrationClientError::Conflict);
        }
        if !status.is_success() {
            return Err(RegistrationClientError::Rejected(status.as_u16()));
        }
        let response: AccountRegistrationResponse =
            serde_json::from_slice(&body).map_err(|_| RegistrationClientError::InvalidResponse)?;
        if !response.matches(registration) {
            return Err(RegistrationClientError::InvalidResponse);
        }
        Ok(RegistrationLookup::Found(Box::new(response)))
    }

    pub async fn register(
        &self,
        registration: &AccountRegistration,
    ) -> Result<AccountRegistrationResponse, RegistrationClientError> {
        registration
            .validate()
            .map_err(|_| RegistrationClientError::InvalidResponse)?;
        let body = serde_json::to_vec(registration)
            .map_err(|_| RegistrationClientError::InvalidResponse)?;
        let (status, body) = self.request("POST", REGISTRATION_PATH, body).await?;
        if status == StatusCode::CONFLICT {
            return Err(RegistrationClientError::Conflict);
        }
        if !matches!(status, StatusCode::OK | StatusCode::CREATED) {
            return Err(RegistrationClientError::Rejected(status.as_u16()));
        }
        let response: AccountRegistrationResponse =
            serde_json::from_slice(&body).map_err(|_| RegistrationClientError::InvalidResponse)?;
        if !response.matches(registration) {
            return Err(RegistrationClientError::InvalidResponse);
        }
        Ok(response)
    }

    pub async fn execution(
        &self,
        account: &ExecutionAccountRecord,
    ) -> Result<AccountExecutionResponse, RegistrationClientError> {
        let path = format!("{EXECUTION_PATH}/{}", account.id);
        let (status, body) = self.request("GET", &path, Vec::new()).await?;
        if !status.is_success() {
            return Err(RegistrationClientError::Rejected(status.as_u16()));
        }
        let response: AccountExecutionResponse =
            serde_json::from_slice(&body).map_err(|_| RegistrationClientError::InvalidResponse)?;
        if !response.matches(account) {
            return Err(RegistrationClientError::InvalidResponse);
        }
        Ok(response)
    }

    async fn request(
        &self,
        method: &str,
        path: &str,
        body: Vec<u8>,
    ) -> Result<(StatusCode, Vec<u8>), RegistrationClientError> {
        let base_url = self
            .base_url
            .as_ref()
            .ok_or(RegistrationClientError::Disabled)?;
        let hmac_key = self
            .hmac_key
            .as_ref()
            .ok_or(RegistrationClientError::Disabled)?;
        let timestamp = Utc::now().timestamp().to_string();
        let nonce = Uuid::new_v4().simple().to_string();
        let canonical = format!(
            "{method}\n{path}\n{}\n{timestamp}\n{nonce}\n{}",
            self.caller_id,
            hex::encode(Sha256::digest(&body))
        );
        let mut mac = HmacSha256::new_from_slice(hmac_key)
            .map_err(|_| RegistrationClientError::InvalidResponse)?;
        mac.update(canonical.as_bytes());
        let signature = hex::encode(mac.finalize().into_bytes());
        let url = base_url
            .join(path)
            .map_err(|_| RegistrationClientError::InvalidResponse)?;
        let builder = match method {
            "GET" => self.client.get(url),
            "POST" => self.client.post(url),
            _ => return Err(RegistrationClientError::InvalidResponse),
        };
        let mut response = builder
            .header("Content-Type", "application/json")
            .header("X-RTC-Caller", &self.caller_id)
            .header("X-RTC-Timestamp", timestamp)
            .header("X-RTC-Nonce", &nonce)
            .header("X-RTC-Signature", signature)
            .body(body)
            .send()
            .await
            .map_err(|_| RegistrationClientError::Transport)?;
        let response_signature = response
            .headers()
            .get("X-RTC-Response-Signature")
            .and_then(|value| value.to_str().ok())
            .map(str::to_owned);
        if response
            .content_length()
            .is_some_and(|length| length > MAX_RESPONSE_BYTES as u64)
        {
            return Err(RegistrationClientError::InvalidResponse);
        }
        let status = response.status();
        let mut body = Vec::new();
        while let Some(chunk) = response
            .chunk()
            .await
            .map_err(|_| RegistrationClientError::Transport)?
        {
            if body.len().saturating_add(chunk.len()) > MAX_RESPONSE_BYTES {
                return Err(RegistrationClientError::InvalidResponse);
            }
            body.extend_from_slice(&chunk);
        }
        verify_response_signature(
            hmac_key,
            path,
            &self.caller_id,
            &nonce,
            status,
            &body,
            response_signature.as_deref(),
        )?;
        Ok((status, body))
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
) -> Result<(), RegistrationClientError> {
    let provided = signature
        .ok_or(RegistrationClientError::InvalidResponse)
        .and_then(|value| {
            hex::decode(value).map_err(|_| RegistrationClientError::InvalidResponse)
        })?;
    let canonical = format!(
        "RESPONSE\n{path}\n{caller}\n{nonce}\n{}\n{}",
        status.as_u16(),
        hex::encode(Sha256::digest(body)),
    );
    let mut mac =
        HmacSha256::new_from_slice(key).map_err(|_| RegistrationClientError::InvalidResponse)?;
    mac.update(canonical.as_bytes());
    mac.verify_slice(&provided)
        .map_err(|_| RegistrationClientError::InvalidResponse)
}

fn bounded_decimal(value: &str) -> bool {
    !value.is_empty() && value.len() <= 40 && value.bytes().all(|byte| byte.is_ascii_digit())
}

fn bounded_notional(value: &str) -> bool {
    bounded_decimal(value)
        && value
            .parse::<u64>()
            .is_ok_and(|notional| notional <= 25_000_000)
}

fn valid_bytes32(value: &str) -> bool {
    value.len() == 66
        && value.starts_with("0x")
        && value[2..]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        && value[2..].bytes().any(|byte| byte != b'0')
}

pub fn spawn(store: ProductStore, client: CoordinatorRegistrationClient, worker_id: String) {
    if !store.is_enabled() || !client.is_enabled() {
        log::info!("coordinator account registration worker disabled by config");
        return;
    }
    tokio::spawn(async move {
        loop {
            if let Err(error) = store.enqueue_ready_account_registrations(100).await {
                log::error!("account registration enqueue failed: {error}");
            }
            match store.claim_account_registrations(&worker_id, 25).await {
                Ok(items) => {
                    for item in items {
                        process(&store, &client, &item).await;
                    }
                }
                Err(error) => log::error!("account registration queue unavailable: {error}"),
            }
            tokio::time::sleep(Duration::from_secs(1)).await;
        }
    });
}

async fn process(
    store: &ProductStore,
    client: &CoordinatorRegistrationClient,
    registration: &AccountRegistration,
) {
    let result = match client.lookup(registration).await {
        Ok(RegistrationLookup::Found(response)) => Ok(*response),
        Ok(RegistrationLookup::Missing) => client.register(registration).await,
        Err(error) => Err(error),
    };
    match result {
        Ok(response) => {
            if let Err(error) = store
                .complete_account_registration(registration, &response)
                .await
            {
                log::error!(
                    "account registration {} completion failed: {error}",
                    registration.execution_account_id
                );
            }
        }
        Err(RegistrationClientError::Conflict | RegistrationClientError::InvalidResponse)
        | Err(RegistrationClientError::Rejected(400 | 409 | 422)) => {
            if let Err(error) = store
                .block_account_registration(
                    registration.execution_account_id,
                    "coordinator_registration_conflict",
                )
                .await
            {
                log::error!(
                    "account registration {} block failed: {error}",
                    registration.execution_account_id
                );
            }
        }
        Err(error) => {
            if let Err(retry_error) = store
                .retry_account_registration(registration.execution_account_id, &error.to_string())
                .await
            {
                log::error!(
                    "account registration {} retry failed: {retry_error}",
                    registration.execution_account_id
                );
            }
        }
    }
}

fn valid_address(value: &str) -> bool {
    value.len() == 42
        && value.starts_with("0x")
        && value[2..]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        && !value[2..].bytes().all(|byte| byte == b'0')
}

#[cfg(test)]
mod tests {
    use super::*;

    fn registration() -> AccountRegistration {
        let mut value = AccountRegistration {
            execution_account_id: Uuid::new_v4(),
            agent_id: Uuid::new_v4(),
            strategy_version: "basis-aapl-v1".into(),
            risk_version: "basis-aapl-v1".into(),
            strategy_manifest_sha256:
                "27df8d5a56b45f6966f8a60d866a55cfddfc65835216def5def023126c96c937".into(),
            lighter_account_index: 7,
            lighter_api_key_index: 254,
            robinhood_owner: "0x1111111111111111111111111111111111111111".into(),
            robinhood_vault: "0x2222222222222222222222222222222222222222".into(),
            robinhood_signer: "0x3333333333333333333333333333333333333333".into(),
            binding_sha256: String::new(),
        };
        value.binding_sha256 = value.calculate_binding_sha256();
        value
    }

    #[test]
    fn digest_binds_every_public_identity() {
        let mut value = registration();
        assert!(value.validate().is_ok());
        let digest = value.binding_sha256.clone();
        value.robinhood_signer = "0x4444444444444444444444444444444444444444".into();
        assert_ne!(digest, value.calculate_binding_sha256());
        assert!(value.validate().is_err());
    }

    #[test]
    fn reserved_lighter_api_key_is_rejected() {
        let mut value = registration();
        value.lighter_api_key_index = 3;
        value.binding_sha256 = value.calculate_binding_sha256();
        assert!(value.validate().is_err());
    }

    #[test]
    fn configuration_is_all_or_nothing() {
        assert!(
            !CoordinatorRegistrationClient::new("", "product-account-provisioner", "")
                .unwrap()
                .is_enabled()
        );
        assert!(CoordinatorRegistrationClient::new(
            "coordinator:8080",
            "product-account-provisioner",
            "00"
        )
        .is_err());
    }

    #[test]
    fn response_signature_binds_registration_request_and_result() {
        let key = [0x42; 32];
        let path = "/v1/account-registrations/account-id";
        let caller = "product-account-provisioner";
        let nonce = "1234567890abcdef1234567890abcdef";
        let status = StatusCode::OK;
        let body = br#"{"status":"active"}"#;
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
            Some(&signature)
        )
        .is_ok());
        assert!(verify_response_signature(
            &key,
            "/v1/account-executions/account-id",
            caller,
            nonce,
            status,
            body,
            Some(&signature)
        )
        .is_err());
        assert!(verify_response_signature(
            &key,
            path,
            caller,
            nonce,
            StatusCode::CREATED,
            body,
            Some(&signature)
        )
        .is_err());
        assert!(verify_response_signature(
            &key,
            path,
            caller,
            nonce,
            status,
            b"{}",
            Some(&signature)
        )
        .is_err());
        assert!(verify_response_signature(&key, path, caller, nonce, status, body, None).is_err());
    }

    #[test]
    fn execution_status_is_bound_to_the_product_account() {
        let registration = registration();
        let account = ExecutionAccountRecord {
            id: registration.execution_account_id,
            agent_id: registration.agent_id,
            strategy_version: registration.strategy_version.clone(),
            strategy_manifest_sha256: registration.strategy_manifest_sha256.clone(),
            chain_id: 4663,
            status: "ready".into(),
            created_at: Utc::now(),
            updated_at: Utc::now(),
        };
        let mut response = AccountExecutionResponse {
            schema_version: 1,
            execution_account_id: account.id.to_string(),
            agent_id: account.agent_id.to_string(),
            strategy_version: account.strategy_version.clone(),
            strategy_manifest_sha256: account.strategy_manifest_sha256.clone(),
            account_status: "active".into(),
            control_mode: "ACTIVE".into(),
            active: true,
            flat: false,
            intent_id: Some(format!("0x{}", "a".repeat(64))),
            symbol: Some("AAPL".into()),
            state: "hedged".into(),
            spot_amount_raw: "100000000000000000".into(),
            spot_decimals: 18,
            perp_open_base: "100".into(),
            perp_base_decimals: 3,
            spot_notional_micros: "25000000".into(),
            perp_notional_micros: "25000000".into(),
            lighter_order_id: Some("lighter-order-1".into()),
            lighter_transaction_hash: Some(format!("0x{}", "b".repeat(64))),
            robinhood_transaction_hash: Some(format!("0x{}", "c".repeat(64))),
            lighter_unwind_order_id: None,
            lighter_unwind_transaction_hash: None,
            robinhood_unwind_transaction_hash: None,
            updated_at_ms: 1,
        };
        assert!(response.matches(&account));
        response.agent_id = Uuid::new_v4().to_string();
        assert!(!response.matches(&account));
        response.agent_id = account.agent_id.to_string();
        response.spot_notional_micros = "25000001".into();
        assert!(!response.matches(&account));
        response.spot_notional_micros = "25000000".into();
        response.active = false;
        assert!(!response.matches(&account));
        response.active = true;
        response.lighter_transaction_hash = None;
        assert!(!response.matches(&account));
        response.lighter_order_id = None;
        response.lighter_transaction_hash = Some(format!("0x{}", "b".repeat(64)));
        assert!(!response.matches(&account));
        response.lighter_order_id = Some("lighter-order-1".into());
        response.robinhood_transaction_hash = None;
        assert!(!response.matches(&account));
        response.robinhood_transaction_hash = Some(format!("0x{}", "c".repeat(64)));
        response.state = "closed".into();
        response.active = false;
        response.flat = true;
        response.spot_amount_raw = "0".into();
        response.perp_open_base = "0".into();
        response.lighter_unwind_order_id = Some("lighter-unwind-1".into());
        response.lighter_unwind_transaction_hash = Some(format!("0x{}", "d".repeat(64)));
        response.robinhood_unwind_transaction_hash = Some(format!("0x{}", "e".repeat(64)));
        assert!(response.matches(&account));
        response.robinhood_unwind_transaction_hash = None;
        assert!(!response.matches(&account));
    }

    #[test]
    fn empty_execution_status_is_strictly_flat() {
        let registration = registration();
        let account = ExecutionAccountRecord {
            id: registration.execution_account_id,
            agent_id: registration.agent_id,
            strategy_version: registration.strategy_version.clone(),
            strategy_manifest_sha256: registration.strategy_manifest_sha256.clone(),
            chain_id: 4663,
            status: "ready".into(),
            created_at: Utc::now(),
            updated_at: Utc::now(),
        };
        let mut response = AccountExecutionResponse {
            schema_version: 1,
            execution_account_id: account.id.to_string(),
            agent_id: account.agent_id.to_string(),
            strategy_version: account.strategy_version.clone(),
            strategy_manifest_sha256: account.strategy_manifest_sha256.clone(),
            account_status: "active".into(),
            control_mode: "REDUCE_ONLY".into(),
            active: false,
            flat: true,
            intent_id: None,
            symbol: None,
            state: "flat".into(),
            spot_amount_raw: "0".into(),
            spot_decimals: 0,
            perp_open_base: "0".into(),
            perp_base_decimals: 0,
            spot_notional_micros: "0".into(),
            perp_notional_micros: "0".into(),
            lighter_order_id: None,
            lighter_transaction_hash: None,
            robinhood_transaction_hash: None,
            lighter_unwind_order_id: None,
            lighter_unwind_transaction_hash: None,
            robinhood_unwind_transaction_hash: None,
            updated_at_ms: 0,
        };
        assert!(response.matches(&account));
        response.active = true;
        assert!(!response.matches(&account));
    }
}
