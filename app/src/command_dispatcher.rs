use crate::product::{AgentCommandWorkItem, OwnerAction};
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

const COMMAND_PATH: &str = "/v1/account-commands";
const STATUS_PATH: &str = "/v1/account-command-status";
const ROBINHOOD_CHAIN_ID: u64 = 4663;
const EMERGENCY_HALT_CALL: &str = "0x51755334";
const WITHDRAW_SETTLEMENT_SELECTOR: &str = "0x142834dd";

#[derive(Clone)]
pub struct CoordinatorCommandClient {
    client: Client,
    base_url: Option<Url>,
    caller_id: String,
    hmac_key: Option<[u8; 32]>,
}

#[derive(Debug, Error)]
pub enum CommandClientError {
    #[error("coordinator command dispatch is disabled")]
    Disabled,
    #[error("coordinator command request failed")]
    Transport,
    #[error("coordinator rejected command request with status {0}")]
    Rejected(u16),
    #[error("coordinator returned an invalid response")]
    InvalidResponse,
    #[error("coordinator returned a different command binding")]
    IdentityMismatch,
}

#[derive(Debug)]
pub enum CommandLookup {
    Found(CoordinatorCommandResponse),
    Missing,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct CoordinatorCommandResponse {
    pub command_id: String,
    pub execution_account_id: String,
    pub command: String,
    pub status: String,
    pub control_mode: String,
    pub reconciled_flat: bool,
    pub evidence_sha256: Option<String>,
    pub owner_actions: Vec<OwnerAction>,
}

#[derive(Serialize)]
struct CommandRequest<'a> {
    command_id: &'a str,
    execution_account_id: &'a str,
    agent_id: &'a str,
    command: &'a str,
    requested_at_ms: u64,
}

#[derive(Serialize)]
struct StatusRequest<'a> {
    command_id: &'a str,
    execution_account_id: &'a str,
}

impl CoordinatorCommandClient {
    pub fn new(base_url: &str, caller_id: &str, hmac_key_hex: &str) -> Result<Self> {
        if base_url.trim().is_empty() && hmac_key_hex.trim().is_empty() {
            return Ok(Self::disabled());
        }
        if base_url.trim().is_empty() || hmac_key_hex.trim().is_empty() {
            return Err(anyhow!("incomplete coordinator command configuration"));
        }
        if !(3..=64).contains(&caller_id.len())
            || !caller_id
                .bytes()
                .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
        {
            return Err(anyhow!("invalid coordinator command caller id"));
        }
        let normalized = if base_url.starts_with("http://") || base_url.starts_with("https://") {
            base_url.to_string()
        } else {
            format!("http://{base_url}")
        };
        let mut url = Url::parse(&normalized)
            .map_err(|_| anyhow!("invalid coordinator command service URL"))?;
        if url.host_str().is_none() || url.query().is_some() || url.fragment().is_some() {
            return Err(anyhow!("invalid coordinator command service URL"));
        }
        url.set_path("");
        let hmac_key: [u8; 32] = hex::decode(hmac_key_hex)
            .map_err(|_| anyhow!("invalid coordinator command HMAC key"))?
            .try_into()
            .map_err(|_| anyhow!("invalid coordinator command HMAC key"))?;
        let client = Client::builder()
            .connect_timeout(Duration::from_secs(3))
            .timeout(Duration::from_secs(10))
            .build()?;
        Ok(Self {
            client,
            base_url: Some(url),
            caller_id: caller_id.to_string(),
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

    pub async fn submit(
        &self,
        item: &AgentCommandWorkItem,
    ) -> Result<CoordinatorCommandResponse, CommandClientError> {
        let requested_at_ms =
            u64::try_from(item.requested_at_ms).map_err(|_| CommandClientError::InvalidResponse)?;
        let command_id = item.id.to_string();
        let execution_account_id = item.execution_account_id.to_string();
        let agent_id = item.agent_id.to_string();
        let request = CommandRequest {
            command_id: &command_id,
            execution_account_id: &execution_account_id,
            agent_id: &agent_id,
            command: &item.command,
            requested_at_ms,
        };
        let response = self.post(COMMAND_PATH, &request, false).await?;
        let CommandLookup::Found(response) = response else {
            return Err(CommandClientError::InvalidResponse);
        };
        validate_response(item, &response)?;
        Ok(response)
    }

    pub async fn status(
        &self,
        item: &AgentCommandWorkItem,
    ) -> Result<CommandLookup, CommandClientError> {
        let command_id = item.id.to_string();
        let execution_account_id = item.execution_account_id.to_string();
        let request = StatusRequest {
            command_id: &command_id,
            execution_account_id: &execution_account_id,
        };
        let response = self.post(STATUS_PATH, &request, true).await?;
        if let CommandLookup::Found(response) = &response {
            validate_response(item, response)?;
        }
        Ok(response)
    }

    async fn post<T: Serialize>(
        &self,
        path: &str,
        request: &T,
        missing_on_bad_request: bool,
    ) -> Result<CommandLookup, CommandClientError> {
        let base_url = self.base_url.as_ref().ok_or(CommandClientError::Disabled)?;
        let hmac_key = self.hmac_key.as_ref().ok_or(CommandClientError::Disabled)?;
        let body = serde_json::to_vec(request).map_err(|_| CommandClientError::InvalidResponse)?;
        let timestamp = Utc::now().timestamp().to_string();
        let nonce = Uuid::new_v4().simple().to_string();
        let digest = Sha256::digest(&body);
        let canonical = format!(
            "POST\n{path}\n{}\n{timestamp}\n{nonce}\n{}",
            self.caller_id,
            hex::encode(digest)
        );
        let mut mac = HmacSha256::new_from_slice(hmac_key)
            .map_err(|_| CommandClientError::InvalidResponse)?;
        mac.update(canonical.as_bytes());
        let signature = hex::encode(mac.finalize().into_bytes());
        let url = base_url
            .join(path)
            .map_err(|_| CommandClientError::InvalidResponse)?;
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
            .await
            .map_err(|_| CommandClientError::Transport)?;
        let status = response.status();
        if missing_on_bad_request && status == StatusCode::BAD_REQUEST {
            return Ok(CommandLookup::Missing);
        }
        if !status.is_success() {
            return Err(CommandClientError::Rejected(status.as_u16()));
        }
        if response
            .content_length()
            .is_some_and(|length| length > 64 << 10)
        {
            return Err(CommandClientError::InvalidResponse);
        }
        let bytes = response
            .bytes()
            .await
            .map_err(|_| CommandClientError::Transport)?;
        if bytes.len() > 64 << 10 {
            return Err(CommandClientError::InvalidResponse);
        }
        let response =
            serde_json::from_slice(&bytes).map_err(|_| CommandClientError::InvalidResponse)?;
        Ok(CommandLookup::Found(response))
    }
}

fn validate_response(
    item: &AgentCommandWorkItem,
    response: &CoordinatorCommandResponse,
) -> Result<(), CommandClientError> {
    if response.command_id != item.id.to_string()
        || response.execution_account_id != item.execution_account_id.to_string()
        || response.command != item.command
    {
        return Err(CommandClientError::IdentityMismatch);
    }
    if !matches!(
        response.status.as_str(),
        "processing" | "reducing" | "awaiting_owner_signature" | "completed" | "blocked"
    ) || !matches!(
        response.control_mode.as_str(),
        "ACTIVE" | "REDUCE_ONLY" | "HALTED"
    ) {
        return Err(CommandClientError::InvalidResponse);
    }
    if response
        .evidence_sha256
        .as_deref()
        .is_some_and(|value| !valid_sha256(value))
    {
        return Err(CommandClientError::InvalidResponse);
    }
    if response.status == "awaiting_owner_signature" {
        if response.command != "withdraw"
            || !response.reconciled_flat
            || response.owner_actions.is_empty()
            || response.evidence_sha256.is_none()
            || !valid_owner_actions(item, &response.owner_actions)
        {
            return Err(CommandClientError::InvalidResponse);
        }
    } else if !response.owner_actions.is_empty() {
        return Err(CommandClientError::InvalidResponse);
    }
    if response.status == "completed" && !response.reconciled_flat {
        return Err(CommandClientError::InvalidResponse);
    }
    Ok(())
}

pub fn spawn(store: ProductStore, client: CoordinatorCommandClient, worker_id: String) {
    if !store.is_enabled() || !client.is_enabled() {
        log::info!("agent command dispatcher disabled by config");
        return;
    }
    tokio::spawn(async move {
        loop {
            let recovered = store.recover_agent_commands(25).await;
            process_batch(&store, &client, recovered).await;
            let claimed = store.claim_agent_commands(&worker_id, 25).await;
            process_batch(&store, &client, claimed).await;
            tokio::time::sleep(Duration::from_secs(1)).await;
        }
    });
}

async fn process_batch(
    store: &ProductStore,
    client: &CoordinatorCommandClient,
    batch: Result<Vec<AgentCommandWorkItem>>,
) {
    let items = match batch {
        Ok(items) => items,
        Err(error) => {
            log::error!("agent command queue unavailable: {error}");
            return;
        }
    };
    for item in items {
        if let Err(error) = process_item(store, client, &item).await {
            log::error!("agent command {} dispatch failed: {error}", item.id);
        }
    }
}

async fn process_item(
    store: &ProductStore,
    client: &CoordinatorCommandClient,
    item: &AgentCommandWorkItem,
) -> Result<()> {
    let response = match client.status(item).await {
        Ok(CommandLookup::Found(response)) => response,
        Ok(CommandLookup::Missing) => match client.submit(item).await {
            Ok(response) => response,
            Err(CommandClientError::IdentityMismatch | CommandClientError::InvalidResponse) => {
                fail_invalid_response(store, item, "coordinator_invalid_response").await?;
                return Ok(());
            }
            Err(CommandClientError::Rejected(400 | 404 | 409 | 422)) => {
                fail_invalid_response(store, item, "coordinator_rejected_command").await?;
                return Ok(());
            }
            Err(error) => return Err(error.into()),
        },
        Err(CommandClientError::IdentityMismatch | CommandClientError::InvalidResponse) => {
            fail_invalid_response(store, item, "coordinator_invalid_response").await?;
            return Ok(());
        }
        Err(CommandClientError::Rejected(409)) => {
            fail_invalid_response(store, item, "coordinator_command_conflict").await?;
            return Ok(());
        }
        Err(error) => return Err(error.into()),
    };
    apply_response(store, item, &response).await
}

async fn apply_response(
    store: &ProductStore,
    item: &AgentCommandWorkItem,
    response: &CoordinatorCommandResponse,
) -> Result<()> {
    let evidence = response
        .evidence_sha256
        .clone()
        .unwrap_or_else(|| response_digest(response));
    match response.status.as_str() {
        "completed" => {
            store
                .complete_reconciled_agent_command(item.id, &evidence, None)
                .await?;
        }
        "blocked" => {
            store
                .complete_reconciled_agent_command(
                    item.id,
                    &evidence,
                    Some("coordinator blocked the command"),
                )
                .await?;
        }
        "awaiting_owner_signature" => {
            store
                .await_agent_command_signature(item.id, &evidence, &response.owner_actions)
                .await?;
        }
        "processing" | "reducing" => {}
        _ => return Err(anyhow!("invalid coordinator command status")),
    }
    Ok(())
}

async fn fail_invalid_response(
    store: &ProductStore,
    item: &AgentCommandWorkItem,
    reason: &str,
) -> Result<()> {
    let digest = hex::encode(Sha256::digest(format!(
        "robin.command-dispatch-failure.v1\n{}\n{}\n{reason}",
        item.id, item.execution_account_id
    )));
    store
        .complete_reconciled_agent_command(item.id, &digest, Some(reason))
        .await?;
    Ok(())
}

fn response_digest(response: &CoordinatorCommandResponse) -> String {
    let encoded = serde_json::to_vec(response).expect("command response is serializable");
    hex::encode(Sha256::digest(encoded))
}

fn valid_sha256(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn valid_owner_actions(item: &AgentCommandWorkItem, actions: &[OwnerAction]) -> bool {
    if !(1..=2).contains(&actions.len())
        || actions.iter().any(|action| {
            action.chain_id != ROBINHOOD_CHAIN_ID
                || action.from != item.robinhood_owner
                || action.to != item.robinhood_vault
                || action.value != "0"
        })
    {
        return false;
    }
    let withdraw = actions.last().expect("owner action count is checked");
    let encoded_amount = withdraw.data.strip_prefix(WITHDRAW_SETTLEMENT_SELECTOR);
    if encoded_amount.is_none_or(|value| {
        value.len() != 64
            || !value
                .bytes()
                .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
            || value.bytes().all(|byte| byte == b'0')
    }) {
        return false;
    }
    actions.len() == 1 || actions[0].data == EMERGENCY_HALT_CALL
}

#[cfg(test)]
mod tests {
    use super::*;

    fn item() -> AgentCommandWorkItem {
        AgentCommandWorkItem {
            id: Uuid::nil(),
            agent_id: Uuid::nil(),
            execution_account_id: Uuid::nil(),
            command: "withdraw".into(),
            agent_status: "closed".into(),
            target_agent_status: "closed".into(),
            requested_at_ms: 1,
            robinhood_owner: "0x1111111111111111111111111111111111111111".into(),
            robinhood_vault: "0x2222222222222222222222222222222222222222".into(),
        }
    }

    #[test]
    fn configuration_is_all_or_nothing() {
        assert!(
            !CoordinatorCommandClient::new("", "product-command-worker", "")
                .unwrap()
                .is_enabled()
        );
        assert!(
            CoordinatorCommandClient::new("internal:8080", "product-command-worker", "00").is_err()
        );
    }

    #[test]
    fn owner_actions_are_only_accepted_for_flat_withdrawal() {
        let item = item();
        let mut response = CoordinatorCommandResponse {
            command_id: item.id.to_string(),
            execution_account_id: item.execution_account_id.to_string(),
            command: item.command.clone(),
            status: "awaiting_owner_signature".into(),
            control_mode: "HALTED".into(),
            reconciled_flat: true,
            evidence_sha256: Some("a".repeat(64)),
            owner_actions: vec![OwnerAction {
                chain_id: ROBINHOOD_CHAIN_ID,
                from: item.robinhood_owner.clone(),
                to: item.robinhood_vault.clone(),
                data: format!("{WITHDRAW_SETTLEMENT_SELECTOR}{:064x}", 25_000_000),
                value: "0".into(),
            }],
        };
        assert!(validate_response(&item, &response).is_ok());
        response.owner_actions[0].from = "0x3333333333333333333333333333333333333333".into();
        assert!(matches!(
            validate_response(&item, &response),
            Err(CommandClientError::InvalidResponse)
        ));
        response.owner_actions[0].from = item.robinhood_owner.clone();
        response.owner_actions[0].to = "0x4444444444444444444444444444444444444444".into();
        assert!(matches!(
            validate_response(&item, &response),
            Err(CommandClientError::InvalidResponse)
        ));
        response.owner_actions[0].to = item.robinhood_vault.clone();
        response.owner_actions.insert(
            0,
            OwnerAction {
                chain_id: ROBINHOOD_CHAIN_ID,
                from: item.robinhood_owner.clone(),
                to: item.robinhood_vault.clone(),
                data: EMERGENCY_HALT_CALL.into(),
                value: "0".into(),
            },
        );
        assert!(validate_response(&item, &response).is_ok());
        response.owner_actions.swap(0, 1);
        assert!(matches!(
            validate_response(&item, &response),
            Err(CommandClientError::InvalidResponse)
        ));
        response.command = "pause".into();
        assert!(matches!(
            validate_response(&item, &response),
            Err(CommandClientError::IdentityMismatch)
        ));
    }
}
