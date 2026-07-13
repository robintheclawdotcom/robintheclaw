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

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DashboardSnapshot {
    pub environment: String,
    pub as_of: DateTime<Utc>,
    pub infrastructure_ready: bool,
    pub policy_id: Option<String>,
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
    pub policy_id: String,
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
