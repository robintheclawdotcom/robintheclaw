use crate::product::{
    ActivityPage, ActivityRecord, AgentRecord, AgentSnapshot, ConfirmedVault, IdentitySnapshot,
    MeResponse, PreferencesInput, PreferencesRecord, SmartAccountRecord, UserRecord, VaultRecord,
    WalletRecord,
};
use anyhow::{anyhow, Result};
use chrono::{DateTime, Utc};
use sha3::{Digest, Keccak256};
use sqlx::postgres::PgPoolOptions;
use sqlx::{PgPool, Postgres, Transaction};
use uuid::Uuid;

#[derive(Clone, Default)]
pub struct ProductStore {
    pool: Option<PgPool>,
}

pub struct ContractActivity {
    pub user_id: Uuid,
    pub chain_id: u64,
    pub kind: String,
    pub transaction_hash: String,
    pub block_number: u64,
    pub log_index: u64,
    pub payload: serde_json::Value,
}

impl ProductStore {
    pub fn disabled() -> Self {
        Self::default()
    }

    pub async fn connect(database_url: &str) -> Result<Self> {
        let pool = PgPoolOptions::new()
            .max_connections(10)
            .connect(database_url)
            .await?;
        sqlx::migrate!("./migrations").run(&pool).await?;
        Ok(Self { pool: Some(pool) })
    }

    pub fn is_enabled(&self) -> bool {
        self.pool.is_some()
    }

    pub async fn ensure_user(&self, did: &str) -> Result<UserRecord> {
        let pool = self.pool()?;
        let id = Uuid::new_v4();
        let user = sqlx::query_as::<_, UserRecord>(
            r#"
            INSERT INTO users (id, privy_did)
            VALUES ($1, $2)
            ON CONFLICT (privy_did) DO UPDATE SET updated_at = now()
            RETURNING id, privy_did, onboarding_state, has_recovery, created_at, updated_at
            "#,
        )
        .bind(id)
        .bind(did)
        .fetch_one(pool)
        .await?;
        self.ensure_preferences(user.id).await?;
        Ok(user)
    }

    pub async fn sync_identity(
        &self,
        did: &str,
        identity: &IdentitySnapshot,
        chain_id: u64,
    ) -> Result<MeResponse> {
        let pool = self.pool()?;
        let mut tx = pool.begin().await?;
        let user = upsert_identity_user(&mut tx, did, identity.has_recovery).await?;

        for wallet in &identity.wallets {
            if let Some(existing_user) = sqlx::query_scalar::<_, Uuid>(
                "SELECT user_id FROM wallet_links WHERE chain_namespace = 'eip155' AND address = $1",
            )
            .bind(&wallet.address)
            .fetch_optional(&mut *tx)
            .await?
            {
                if existing_user != user.id {
                    return Err(anyhow!("wallet is linked to another account"));
                }
            }

            sqlx::query(
                r#"
                INSERT INTO wallet_links (
                    id, user_id, chain_namespace, address, wallet_type, is_primary, verified_at
                ) VALUES ($1, $2, 'eip155', $3, $4, $5, $6)
                ON CONFLICT (chain_namespace, address) DO UPDATE SET
                    wallet_type = EXCLUDED.wallet_type,
                    is_primary = EXCLUDED.is_primary,
                    verified_at = EXCLUDED.verified_at
                "#,
            )
            .bind(Uuid::new_v4())
            .bind(user.id)
            .bind(&wallet.address)
            .bind(&wallet.wallet_type)
            .bind(identity.embedded_wallet.as_deref() == Some(wallet.address.as_str()))
            .bind(wallet.verified_at)
            .execute(&mut *tx)
            .await?;
        }

        let addresses: Vec<&str> = identity
            .wallets
            .iter()
            .map(|w| w.address.as_str())
            .collect();
        if addresses.is_empty() {
            sqlx::query("DELETE FROM wallet_links WHERE user_id = $1")
                .bind(user.id)
                .execute(&mut *tx)
                .await?;
        } else {
            sqlx::query("DELETE FROM wallet_links WHERE user_id = $1 AND NOT (address = ANY($2))")
                .bind(user.id)
                .bind(&addresses)
                .execute(&mut *tx)
                .await?;
        }

        if let Some(address) = &identity.embedded_wallet {
            sqlx::query(
                r#"
                INSERT INTO smart_accounts (user_id, chain_id, address, provider)
                VALUES ($1, $2, $3, 'alchemy-eip7702')
                ON CONFLICT (user_id, chain_id) DO UPDATE SET
                    address = EXCLUDED.address,
                    provider = EXCLUDED.provider
                "#,
            )
            .bind(user.id)
            .bind(chain_id as i64)
            .bind(address)
            .execute(&mut *tx)
            .await?;
        }

        sqlx::query(
            r#"
            INSERT INTO preferences (user_id, active_funding_wallet)
            VALUES ($1, $2)
            ON CONFLICT (user_id) DO UPDATE SET
                active_funding_wallet = CASE
                    WHEN EXISTS (
                        SELECT 1 FROM wallet_links
                        WHERE user_id = EXCLUDED.user_id
                          AND address = preferences.active_funding_wallet
                    ) THEN preferences.active_funding_wallet
                    ELSE EXCLUDED.active_funding_wallet
                END,
                updated_at = now()
            "#,
        )
        .bind(user.id)
        .bind(&identity.embedded_wallet)
        .execute(&mut *tx)
        .await?;

        tx.commit().await?;
        self.me(did).await
    }

    pub async fn me(&self, did: &str) -> Result<MeResponse> {
        let user = self.ensure_user(did).await?;
        let pool = self.pool()?;
        let wallets = sqlx::query_as::<_, WalletRecord>(
            r#"
            SELECT id, chain_namespace, address, wallet_type, label, is_primary, verified_at
            FROM wallet_links
            WHERE user_id = $1
            ORDER BY is_primary DESC, verified_at DESC, address
            "#,
        )
        .bind(user.id)
        .fetch_all(pool)
        .await?;
        let smart_account = sqlx::query_as::<_, SmartAccountRecord>(
            r#"
            SELECT chain_id, address, provider, created_at
            FROM smart_accounts
            WHERE user_id = $1
            ORDER BY created_at
            LIMIT 1
            "#,
        )
        .bind(user.id)
        .fetch_optional(pool)
        .await?;
        let preferences = sqlx::query_as::<_, PreferencesRecord>(
            r#"
            SELECT display_currency, active_funding_wallet, notifications_enabled, updated_at
            FROM preferences WHERE user_id = $1
            "#,
        )
        .bind(user.id)
        .fetch_one(pool)
        .await?;
        let vault = self.vault_for_user(user.id).await?;

        Ok(MeResponse {
            user,
            wallets,
            smart_account,
            preferences,
            vault,
        })
    }

    pub async fn update_preferences(
        &self,
        did: &str,
        input: &PreferencesInput,
    ) -> Result<PreferencesRecord> {
        let me = self.me(did).await?;
        if let Some(address) = input.active_funding_wallet.as_deref() {
            let address = normalize_address(address)?;
            let known = me
                .wallets
                .iter()
                .any(|wallet| wallet.address.eq_ignore_ascii_case(&address));
            if !known {
                return Err(anyhow!("funding wallet is not linked to this account"));
            }
        }

        let pool = self.pool()?;
        sqlx::query_as::<_, PreferencesRecord>(
            r#"
            UPDATE preferences SET
                display_currency = $2,
                active_funding_wallet = $3,
                notifications_enabled = $4,
                updated_at = now()
            WHERE user_id = $1
            RETURNING display_currency, active_funding_wallet, notifications_enabled, updated_at
            "#,
        )
        .bind(me.user.id)
        .bind(&input.display_currency)
        .bind(
            input
                .active_funding_wallet
                .as_deref()
                .map(normalize_address)
                .transpose()?,
        )
        .bind(input.notifications_enabled)
        .fetch_one(pool)
        .await
        .map_err(Into::into)
    }

    pub async fn confirm_vault(
        &self,
        did: &str,
        confirmed: &ConfirmedVault,
    ) -> Result<VaultRecord> {
        let pool = self.pool()?;
        let user = self.ensure_user(did).await?;
        let mut tx = pool.begin().await?;
        let vault = sqlx::query_as::<_, VaultRecord>(
            r#"
            INSERT INTO vaults (
                id, user_id, chain_id, factory_version, asset_address, vault_address,
                guard_address, anchor_address, call_id, transaction_hash, status
            ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'ready')
            ON CONFLICT (user_id, chain_id, factory_version) DO UPDATE SET
                asset_address = EXCLUDED.asset_address,
                vault_address = EXCLUDED.vault_address,
                guard_address = EXCLUDED.guard_address,
                anchor_address = EXCLUDED.anchor_address,
                call_id = EXCLUDED.call_id,
                transaction_hash = EXCLUDED.transaction_hash,
                status = EXCLUDED.status,
                updated_at = now()
            RETURNING id, chain_id, factory_version, asset_address, vault_address, guard_address,
                anchor_address, call_id, transaction_hash, status, created_at, updated_at
            "#,
        )
        .bind(Uuid::new_v4())
        .bind(user.id)
        .bind(confirmed.chain_id)
        .bind(confirmed.factory_version)
        .bind(&confirmed.asset_address)
        .bind(&confirmed.vault_address)
        .bind(&confirmed.guard_address)
        .bind(&confirmed.anchor_address)
        .bind(&confirmed.call_id)
        .bind(&confirmed.transaction_hash)
        .fetch_one(&mut *tx)
        .await?;

        sqlx::query(
            "UPDATE users SET onboarding_state = 'complete', updated_at = now() WHERE id = $1",
        )
        .bind(user.id)
        .execute(&mut *tx)
        .await?;
        sqlx::query(
            r#"
            INSERT INTO activity (
                id, user_id, chain_id, kind, transaction_hash, block_number,
                log_index, payload, occurred_at
            ) VALUES ($1, $2, $3, 'vault_created', $4, $5, $6, $7, now())
            ON CONFLICT (chain_id, transaction_hash, log_index) WHERE
                transaction_hash IS NOT NULL AND log_index IS NOT NULL
            DO NOTHING
            "#,
        )
        .bind(Uuid::new_v4())
        .bind(user.id)
        .bind(confirmed.chain_id)
        .bind(&confirmed.transaction_hash)
        .bind(confirmed.block_number)
        .bind(confirmed.log_index)
        .bind(serde_json::json!({
            "vaultAddress": confirmed.vault_address,
            "guardAddress": confirmed.guard_address,
            "anchorAddress": confirmed.anchor_address,
        }))
        .execute(&mut *tx)
        .await?;
        tx.commit().await?;
        Ok(vault)
    }

    pub async fn activity(
        &self,
        did: &str,
        cursor: Option<Uuid>,
        limit: usize,
    ) -> Result<ActivityPage> {
        let pool = self.pool()?;
        let user = self.ensure_user(did).await?;
        let rows = if let Some(cursor) = cursor {
            let cursor_time = sqlx::query_scalar::<_, DateTime<Utc>>(
                "SELECT occurred_at FROM activity WHERE id = $1 AND user_id = $2",
            )
            .bind(cursor)
            .bind(user.id)
            .fetch_optional(pool)
            .await?
            .ok_or_else(|| anyhow!("activity cursor not found"))?;
            sqlx::query_as::<_, ActivityRecord>(
                r#"
                SELECT id, chain_id, kind, transaction_hash, block_number, log_index, payload,
                    occurred_at
                FROM activity
                WHERE user_id = $1 AND (occurred_at, id) < ($2, $3)
                ORDER BY occurred_at DESC, id DESC
                LIMIT $4
                "#,
            )
            .bind(user.id)
            .bind(cursor_time)
            .bind(cursor)
            .bind((limit + 1) as i64)
            .fetch_all(pool)
            .await?
        } else {
            sqlx::query_as::<_, ActivityRecord>(
                r#"
                SELECT id, chain_id, kind, transaction_hash, block_number, log_index, payload,
                    occurred_at
                FROM activity
                WHERE user_id = $1
                ORDER BY occurred_at DESC, id DESC
                LIMIT $2
                "#,
            )
            .bind(user.id)
            .bind((limit + 1) as i64)
            .fetch_all(pool)
            .await?
        };

        let mut items = rows;
        let next_cursor = if items.len() > limit {
            items.truncate(limit);
            items.last().map(|item| item.id)
        } else {
            None
        };
        Ok(ActivityPage { items, next_cursor })
    }

    pub async fn launch_agent(&self, did: &str, strategy_version: &str) -> Result<AgentRecord> {
        let user = self.ensure_user(did).await?;
        if self.vault_for_user(user.id).await?.is_none() {
            return Err(anyhow!("create a vault before launching an agent"));
        }
        let pool = self.pool()?;
        sqlx::query_as::<_, AgentRecord>(
            r#"
            INSERT INTO agents (id, user_id, strategy_version, mode, status)
            VALUES ($1, $2, $3, 'paper', 'running')
            ON CONFLICT (user_id) DO UPDATE SET
                status = 'running',
                updated_at = now()
            RETURNING id, strategy_version, mode, status, created_at, updated_at
            "#,
        )
        .bind(Uuid::new_v4())
        .bind(user.id)
        .bind(strategy_version)
        .fetch_one(pool)
        .await
        .map_err(Into::into)
    }

    pub async fn set_agent_status(
        &self,
        did: &str,
        agent_id: Uuid,
        status: &str,
    ) -> Result<AgentRecord> {
        let user = self.ensure_user(did).await?;
        sqlx::query_as::<_, AgentRecord>(
            r#"
            UPDATE agents SET status = $3, updated_at = now()
            WHERE id = $1 AND user_id = $2
            RETURNING id, strategy_version, mode, status, created_at, updated_at
            "#,
        )
        .bind(agent_id)
        .bind(user.id)
        .bind(status)
        .fetch_optional(self.pool()?)
        .await?
        .ok_or_else(|| anyhow!("agent not found"))
    }

    pub async fn agent_snapshot(&self, user_id: Uuid) -> Result<Option<AgentSnapshot>> {
        let Some(record) = sqlx::query_as::<_, AgentRecord>(
            r#"
            SELECT id, strategy_version, mode, status, created_at, updated_at
            FROM agents WHERE user_id = $1
            "#,
        )
        .bind(user_id)
        .fetch_optional(self.pool()?)
        .await?
        else {
            return Ok(None);
        };
        let (evaluations, candidates, last_evaluated_at) =
            sqlx::query_as::<_, (i64, i64, Option<DateTime<Utc>>)>(
                r#"
            SELECT count(*)::bigint,
                   count(*) FILTER (WHERE status = 'candidate')::bigint,
                   max(evaluated_at)
            FROM agent_paper_events WHERE agent_id = $1
            "#,
            )
            .bind(record.id)
            .fetch_one(self.pool()?)
            .await?;
        Ok(Some(AgentSnapshot {
            record,
            evaluations,
            candidates,
            last_evaluated_at,
        }))
    }

    pub async fn watched_contracts(&self) -> Result<Vec<(Uuid, String)>> {
        let pool = self.pool()?;
        sqlx::query_as::<_, (Uuid, String)>(
            r#"
            SELECT user_id, vault_address FROM vaults
            UNION ALL SELECT user_id, guard_address FROM vaults
            UNION ALL SELECT user_id, anchor_address FROM vaults
            "#,
        )
        .fetch_all(pool)
        .await
        .map_err(Into::into)
    }

    pub async fn user_for_smart_account(
        &self,
        chain_id: u64,
        address: &str,
    ) -> Result<Option<Uuid>> {
        let pool = self.pool()?;
        sqlx::query_scalar(
            "SELECT user_id FROM smart_accounts WHERE chain_id = $1 AND address = $2",
        )
        .bind(chain_id as i64)
        .bind(normalize_address(address)?)
        .fetch_optional(pool)
        .await
        .map_err(Into::into)
    }

    pub async fn activity_cursor(&self, name: &str) -> Result<Option<u64>> {
        let pool = self.pool()?;
        let value =
            sqlx::query_scalar::<_, i64>("SELECT block_number FROM app_cursors WHERE name = $1")
                .bind(name)
                .fetch_optional(pool)
                .await?;
        value
            .map(|block| u64::try_from(block).map_err(Into::into))
            .transpose()
    }

    pub async fn set_activity_cursor(&self, name: &str, block: u64) -> Result<()> {
        let pool = self.pool()?;
        sqlx::query(
            r#"
            INSERT INTO app_cursors (name, block_number) VALUES ($1, $2)
            ON CONFLICT (name) DO UPDATE SET block_number = EXCLUDED.block_number, updated_at = now()
            "#,
        )
        .bind(name)
        .bind(i64::try_from(block)?)
        .execute(pool)
        .await?;
        Ok(())
    }

    pub async fn record_contract_activity(&self, activity: &ContractActivity) -> Result<bool> {
        let pool = self.pool()?;
        let result = sqlx::query(
            r#"
            INSERT INTO activity (
                id, user_id, chain_id, kind, transaction_hash, block_number,
                log_index, payload, occurred_at
            ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
            ON CONFLICT (chain_id, transaction_hash, log_index) WHERE
                transaction_hash IS NOT NULL AND log_index IS NOT NULL
            DO NOTHING
            "#,
        )
        .bind(Uuid::new_v4())
        .bind(activity.user_id)
        .bind(activity.chain_id as i64)
        .bind(&activity.kind)
        .bind(&activity.transaction_hash)
        .bind(i64::try_from(activity.block_number)?)
        .bind(i64::try_from(activity.log_index)?)
        .bind(&activity.payload)
        .execute(pool)
        .await?;
        Ok(result.rows_affected() == 1)
    }

    pub async fn record_metric(
        &self,
        did: &str,
        name: &str,
        duration_ms: Option<u64>,
        status: Option<&str>,
    ) -> Result<()> {
        let pool = self.pool()?;
        let user = self.ensure_user(did).await?;
        sqlx::query(
            "INSERT INTO product_metrics (id, user_id, name, duration_ms, status) VALUES ($1, $2, $3, $4, $5)",
        )
        .bind(Uuid::new_v4())
        .bind(user.id)
        .bind(name)
        .bind(duration_ms.map(i64::try_from).transpose()?)
        .bind(status)
        .execute(pool)
        .await?;
        Ok(())
    }

    async fn ensure_preferences(&self, user_id: Uuid) -> Result<()> {
        let pool = self.pool()?;
        sqlx::query(
            "INSERT INTO preferences (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING",
        )
        .bind(user_id)
        .execute(pool)
        .await?;
        Ok(())
    }

    async fn vault_for_user(&self, user_id: Uuid) -> Result<Option<VaultRecord>> {
        let pool = self.pool()?;
        sqlx::query_as::<_, VaultRecord>(
            r#"
            SELECT id, chain_id, factory_version, asset_address, vault_address, guard_address,
                anchor_address, call_id, transaction_hash, status, created_at, updated_at
            FROM vaults
            WHERE user_id = $1
            ORDER BY factory_version DESC
            LIMIT 1
            "#,
        )
        .bind(user_id)
        .fetch_optional(pool)
        .await
        .map_err(Into::into)
    }

    fn pool(&self) -> Result<&PgPool> {
        self.pool
            .as_ref()
            .ok_or_else(|| anyhow!("application database is not configured"))
    }
}

async fn upsert_identity_user(
    tx: &mut Transaction<'_, Postgres>,
    did: &str,
    has_recovery: bool,
) -> Result<UserRecord> {
    sqlx::query_as::<_, UserRecord>(
        r#"
        INSERT INTO users (id, privy_did, onboarding_state, has_recovery)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (privy_did) DO UPDATE SET
            onboarding_state = CASE
                WHEN users.onboarding_state = 'complete' THEN 'complete'
                ELSE EXCLUDED.onboarding_state
            END,
            has_recovery = EXCLUDED.has_recovery,
            updated_at = now()
        RETURNING id, privy_did, onboarding_state, has_recovery, created_at, updated_at
        "#,
    )
    .bind(Uuid::new_v4())
    .bind(did)
    .bind(if has_recovery { "vault" } else { "recovery" })
    .bind(has_recovery)
    .fetch_one(&mut **tx)
    .await
    .map_err(Into::into)
}

pub fn normalize_address(value: &str) -> Result<String> {
    let address = value.trim().strip_prefix("0x").unwrap_or(value.trim());
    if address.len() != 40 || !address.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err(anyhow!("invalid EVM address"));
    }
    let lower = address.to_ascii_lowercase();
    let digest = Keccak256::digest(lower.as_bytes());
    let mut output = String::with_capacity(42);
    output.push_str("0x");
    for (index, character) in lower.chars().enumerate() {
        let nibble = if index % 2 == 0 {
            digest[index / 2] >> 4
        } else {
            digest[index / 2] & 0x0f
        };
        if character.is_ascii_alphabetic() && nibble >= 8 {
            output.push(character.to_ascii_uppercase());
        } else {
            output.push(character);
        }
    }
    Ok(output)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn checksums_addresses() {
        assert_eq!(
            normalize_address("0x52908400098527886e0f7030069857d2e4169ee7").unwrap(),
            "0x52908400098527886E0F7030069857D2E4169EE7"
        );
    }

    #[test]
    fn rejects_short_addresses() {
        assert!(normalize_address("0x1234").is_err());
    }
}
