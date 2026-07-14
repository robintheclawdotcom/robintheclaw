use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use uuid::Uuid;

#[derive(Clone, Debug)]
pub struct VerifiedWallet {
    pub address: String,
    pub wallet_type: String,
    pub verified_at: DateTime<Utc>,
}

#[derive(Clone, Debug)]
pub struct IdentitySnapshot {
    pub wallets: Vec<VerifiedWallet>,
    pub embedded_wallet: Option<String>,
    pub has_recovery: bool,
}

#[derive(Clone, Debug, Serialize, sqlx::FromRow)]
#[serde(rename_all = "camelCase")]
pub struct UserRecord {
    pub id: Uuid,
    pub privy_did: String,
    pub onboarding_state: String,
    pub has_recovery: bool,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Clone, Debug, Serialize, sqlx::FromRow)]
#[serde(rename_all = "camelCase")]
pub struct WalletRecord {
    pub id: Uuid,
    pub chain_namespace: String,
    pub address: String,
    pub wallet_type: String,
    pub label: Option<String>,
    pub is_primary: bool,
    pub verified_at: DateTime<Utc>,
}

#[derive(Clone, Debug, Serialize, sqlx::FromRow)]
#[serde(rename_all = "camelCase")]
pub struct SmartAccountRecord {
    pub chain_id: i64,
    pub address: String,
    pub provider: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Clone, Debug, Serialize, sqlx::FromRow)]
#[serde(rename_all = "camelCase")]
pub struct PreferencesRecord {
    pub display_currency: String,
    pub active_funding_wallet: Option<String>,
    pub notifications_enabled: bool,
    pub updated_at: DateTime<Utc>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PreferencesInput {
    pub display_currency: String,
    pub active_funding_wallet: Option<String>,
    pub notifications_enabled: bool,
}

#[derive(Clone, Debug, Serialize, sqlx::FromRow)]
#[serde(rename_all = "camelCase")]
pub struct VaultRecord {
    pub id: Uuid,
    pub chain_id: i64,
    pub factory_version: i64,
    pub asset_address: String,
    pub vault_address: String,
    pub guard_address: String,
    pub anchor_address: String,
    pub call_id: String,
    pub transaction_hash: String,
    pub status: String,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Clone, Debug)]
pub struct ConfirmedVault {
    pub chain_id: i64,
    pub factory_version: i64,
    pub asset_address: String,
    pub vault_address: String,
    pub guard_address: String,
    pub anchor_address: String,
    pub call_id: String,
    pub transaction_hash: String,
    pub block_number: i64,
    pub log_index: i64,
}

#[derive(Clone, Debug, Serialize, sqlx::FromRow)]
#[serde(rename_all = "camelCase")]
pub struct ActivityRecord {
    pub id: Uuid,
    pub chain_id: i64,
    pub kind: String,
    pub transaction_hash: Option<String>,
    pub block_number: Option<i64>,
    pub log_index: Option<i64>,
    pub payload: Value,
    pub occurred_at: DateTime<Utc>,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MeResponse {
    pub user: UserRecord,
    pub wallets: Vec<WalletRecord>,
    pub smart_account: Option<SmartAccountRecord>,
    pub preferences: PreferencesRecord,
    pub vault: Option<VaultRecord>,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Amount {
    pub raw: String,
    pub decimals: u8,
    pub symbol: String,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct VaultSnapshot {
    pub record: VaultRecord,
    pub balance: Amount,
    pub halted: bool,
    pub remaining_capacity: Amount,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct WalletBalanceSnapshot {
    pub wallet: WalletRecord,
    pub balance: Amount,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PositionSnapshot {
    pub id: String,
    pub symbol: String,
    pub status: String,
    pub spot_leg: Amount,
    pub perp_leg: Amount,
    pub entry_basis_bps: String,
    pub current_basis_bps: String,
    pub funding: Amount,
    pub pnl: Amount,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OpportunitySnapshot {
    pub symbol: String,
    pub basis_bps: String,
    pub liquidity: String,
    pub observed_at: i64,
}

#[derive(Clone, Debug, Serialize, sqlx::FromRow)]
#[serde(rename_all = "camelCase")]
pub struct AgentRecord {
    pub id: Uuid,
    pub strategy_version: String,
    pub mode: String,
    pub status: String,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

pub const LIVE_STRATEGY_VERSION: &str = "basis-aapl-v1";
pub const LIVE_STRATEGY_MANIFEST_SHA256: &str =
    "da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f";

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AgentCreateInput {
    pub strategy_version: String,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AgentSnapshot {
    #[serde(flatten)]
    pub record: AgentRecord,
    pub evaluations: i64,
    pub candidates: i64,
    pub last_evaluated_at: Option<DateTime<Utc>>,
}

#[derive(Clone, Debug, Serialize, sqlx::FromRow)]
#[serde(rename_all = "camelCase")]
pub struct ExecutionAccountRecord {
    pub id: Uuid,
    pub agent_id: Uuid,
    pub strategy_version: String,
    pub strategy_manifest_sha256: String,
    pub chain_id: i64,
    pub status: String,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AgentExecutionStatus {
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

#[derive(Clone, Debug, Serialize, sqlx::FromRow)]
#[serde(rename_all = "camelCase")]
pub struct ExecutionBindingRecord {
    pub binding_ref: Uuid,
    pub request_id: Uuid,
    pub provider_request_id: Option<Uuid>,
    pub venue: String,
    pub owner_address: String,
    pub lighter_account_index: Option<i64>,
    pub lighter_api_key_index: Option<i16>,
    pub robinhood_vault_address: Option<String>,
    pub robinhood_signer_address: Option<String>,
    pub robinhood_key_version: Option<i64>,
    pub robinhood_factory_address: Option<String>,
    pub robinhood_registry_address: Option<String>,
    pub robinhood_policy_digest: Option<String>,
    pub robinhood_risk_manager_address: Option<String>,
    pub robinhood_spot_adapter_address: Option<String>,
    pub robinhood_deployment_block: Option<i64>,
    pub robinhood_deployment_action: Option<Value>,
    pub robinhood_authorization_transaction_hash: Option<String>,
    pub robinhood_authorization_block: Option<i64>,
    pub public_identifier: Option<String>,
    pub public_key: Option<String>,
    pub association_payload: Option<String>,
    pub proof_transaction_hash: Option<String>,
    pub status: String,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Clone, Debug, Serialize, sqlx::FromRow)]
#[serde(rename_all = "camelCase")]
pub struct AgentReadiness {
    pub execution_account_id: Uuid,
    pub lighter_account_index: Option<i64>,
    pub robinhood_owner_address: Option<String>,
    pub robinhood_vault_address: Option<String>,
    pub robinhood_signer_address: Option<String>,
    pub coordinator_registered: bool,
    pub lighter_linked: bool,
    pub lighter_funded: bool,
    pub robinhood_deployed: bool,
    pub robinhood_funded: bool,
    pub user_gas_ready: bool,
    pub execution_gas_ready: bool,
    pub policy_active: bool,
    pub reconciled: bool,
    pub valid_until: Option<DateTime<Utc>>,
    #[sqlx(skip)]
    pub can_launch: bool,
    #[sqlx(skip)]
    pub blockers: Vec<String>,
}

impl AgentReadiness {
    pub fn finalize(mut self) -> Self {
        let checks = [
            (
                self.coordinator_registered,
                "coordinator_account_not_registered",
            ),
            (self.lighter_linked, "lighter_not_linked"),
            (self.lighter_funded, "lighter_usdc_not_funded"),
            (self.robinhood_deployed, "robinhood_vault_not_deployed"),
            (self.robinhood_funded, "robinhood_usdg_not_funded"),
            (self.user_gas_ready, "user_eth_gas_not_funded"),
            (self.execution_gas_ready, "execution_eth_gas_not_funded"),
            (self.policy_active, "mainnet_policy_not_active"),
            (self.reconciled, "accounts_not_reconciled"),
        ];
        self.blockers = checks
            .into_iter()
            .filter(|(ready, _)| !ready)
            .map(|(_, blocker)| blocker.to_string())
            .collect();
        if self
            .valid_until
            .is_none_or(|deadline| deadline <= Utc::now())
            && self.blockers.is_empty()
        {
            self.blockers.push("readiness_evidence_expired".to_string());
        }
        self.can_launch = self.blockers.is_empty();
        self
    }
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct LighterLinkRequestInput {
    pub owner_address: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct LighterConfirmInput {
    pub request_id: Uuid,
    pub link_id: Uuid,
    pub l1_signature: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct RobinhoodConfirmInput {
    pub request_id: Uuid,
    pub transaction_hash: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AgentCommandInput {
    pub command: String,
}

#[derive(Clone, Debug, Serialize, sqlx::FromRow)]
#[serde(rename_all = "camelCase")]
pub struct AgentCommandRecord {
    pub id: Uuid,
    pub agent_id: Uuid,
    pub execution_account_id: Uuid,
    pub idempotency_key: String,
    pub command: String,
    pub status: String,
    pub agent_status: String,
    pub target_agent_status: String,
    pub error_reason: Option<String>,
    pub result_evidence_digest: Option<String>,
    #[sqlx(json)]
    pub owner_actions: Vec<OwnerAction>,
    pub completed_at: Option<DateTime<Utc>>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Clone, Debug, sqlx::FromRow)]
pub struct AgentCommandWorkItem {
    pub id: Uuid,
    pub agent_id: Uuid,
    pub execution_account_id: Uuid,
    pub command: String,
    pub agent_status: String,
    pub target_agent_status: String,
    pub requested_at_ms: i64,
    pub robinhood_owner: String,
    pub robinhood_vault: String,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case", deny_unknown_fields)]
pub struct OwnerAction {
    pub chain_id: u64,
    pub from: String,
    pub to: String,
    pub data: String,
    pub value: String,
}

pub struct ReadinessEvidenceInput<'a> {
    pub check_name: &'a str,
    pub ready: bool,
    pub source: &'a str,
    pub evidence_digest: &'a str,
    pub observed_at: DateTime<Utc>,
    pub expires_at: DateTime<Utc>,
}

pub fn command_transition(
    status: &str,
    command: &str,
    ready: bool,
    reconciled: bool,
) -> Result<&'static str, &'static str> {
    match (status, command) {
        ("ready", "launch") if ready => Ok("running"),
        ("running", "pause") => Ok("paused"),
        ("paused", "resume") if ready => Ok("running"),
        ("closed", "close") => Ok("closed"),
        ("closing", "close") => Ok("closed"),
        (
            "setup"
            | "provisioning"
            | "awaiting_signatures"
            | "awaiting_funding"
            | "ready"
            | "running"
            | "reducing"
            | "paused"
            | "blocked",
            "close",
        ) => Ok("closed"),
        ("closed", "withdraw") if reconciled => Ok("closed"),
        ("ready", "launch") | ("paused", "resume") => Err("agent_not_ready"),
        ("closed", "withdraw") => Err("accounts_not_reconciled"),
        (_, "launch" | "pause" | "resume" | "close" | "withdraw") => {
            Err("invalid_agent_transition")
        }
        _ => Err("unsupported_command"),
    }
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AgentStatusInput {
    pub status: String,
}

#[cfg(test)]
mod agent_tests {
    use super::*;

    #[test]
    fn readiness_is_fail_closed() {
        let readiness = AgentReadiness {
            execution_account_id: Uuid::nil(),
            lighter_account_index: None,
            robinhood_owner_address: None,
            robinhood_vault_address: None,
            robinhood_signer_address: None,
            coordinator_registered: true,
            lighter_linked: true,
            lighter_funded: true,
            robinhood_deployed: true,
            robinhood_funded: true,
            user_gas_ready: true,
            execution_gas_ready: false,
            policy_active: true,
            reconciled: true,
            valid_until: Some(Utc::now()),
            can_launch: false,
            blockers: Vec::new(),
        }
        .finalize();
        assert!(!readiness.can_launch);
        assert_eq!(readiness.blockers, ["execution_eth_gas_not_funded"]);
    }

    #[test]
    fn live_commands_only_follow_safe_transitions() {
        assert_eq!(
            command_transition("ready", "launch", true, true),
            Ok("running")
        );
        assert_eq!(
            command_transition("running", "pause", true, true),
            Ok("paused")
        );
        assert_eq!(
            command_transition("ready", "launch", false, true),
            Err("agent_not_ready")
        );
        assert_eq!(
            command_transition("running", "resume", true, true),
            Err("invalid_agent_transition")
        );
    }

    #[test]
    fn lighter_link_request_rejects_secret_fields() {
        let input = serde_json::from_value::<LighterLinkRequestInput>(serde_json::json!({
            "ownerAddress": "0x1111111111111111111111111111111111111111"
        }))
        .unwrap();
        assert_eq!(
            input.owner_address,
            "0x1111111111111111111111111111111111111111"
        );
        let payload = serde_json::json!({
            "ownerAddress": "0x1111111111111111111111111111111111111111",
            "ethereumPrivateKey": "never-accepted"
        });
        assert!(serde_json::from_value::<LighterLinkRequestInput>(payload).is_err());

        for field in ["accountIndex", "nonce"] {
            let mut payload = serde_json::json!({
                "ownerAddress": "0x1111111111111111111111111111111111111111"
            });
            payload[field] = serde_json::json!(7);
            assert!(serde_json::from_value::<LighterLinkRequestInput>(payload).is_err());
        }
    }

    #[test]
    fn live_agent_request_accepts_only_the_strategy_version() {
        let input = serde_json::from_value::<AgentCreateInput>(serde_json::json!({
            "strategyVersion": "basis-aapl-v1"
        }))
        .unwrap();
        assert_eq!(input.strategy_version, LIVE_STRATEGY_VERSION);
        assert!(serde_json::from_slice::<AgentCreateInput>(b"").is_err());
        assert!(serde_json::from_slice::<AgentCreateInput>(b"{}").is_err());
        assert!(serde_json::from_slice::<AgentCreateInput>(b"not-json").is_err());
        assert!(
            serde_json::from_value::<AgentCreateInput>(serde_json::json!({
                "strategyVersion": "basis-aapl-v1",
                "strategyManifestSha256": LIVE_STRATEGY_MANIFEST_SHA256
            }))
            .is_err()
        );
    }

    #[test]
    fn robinhood_confirmation_rejects_user_supplied_graph_identity() {
        let input = serde_json::json!({
            "requestId": Uuid::new_v4(),
            "transactionHash": format!("0x{}", "11".repeat(32)),
            "ownerAddress": "0x1111111111111111111111111111111111111111",
            "vaultAddress": "0x2222222222222222222222222222222222222222"
        });
        assert!(serde_json::from_value::<RobinhoodConfirmInput>(input).is_err());
    }
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DashboardSnapshot {
    pub environment: String,
    pub as_of: DateTime<Utc>,
    pub infrastructure_ready: bool,
    pub agent: Option<AgentSnapshot>,
    pub total_value: Amount,
    pub available_balance: Amount,
    pub deployed_capital: Amount,
    pub pnl: Option<Amount>,
    pub smart_account: Option<SmartAccountRecord>,
    pub vault: Option<VaultSnapshot>,
    pub positions: Vec<PositionSnapshot>,
    pub opportunities: Vec<OpportunitySnapshot>,
    pub activity: Vec<ActivityRecord>,
    pub wallets: Vec<WalletBalanceSnapshot>,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TransactionCall {
    pub to: String,
    pub data: String,
    pub value: String,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TransactionPlan {
    pub chain_id: u64,
    pub smart_account: String,
    pub expected_vault: String,
    pub calls: Vec<TransactionCall>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ConfirmVaultInput {
    pub call_id: String,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ActivityPage {
    pub items: Vec<ActivityRecord>,
    pub next_cursor: Option<Uuid>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct MetricInput {
    pub name: String,
    pub duration_ms: Option<u64>,
    pub status: Option<String>,
}
