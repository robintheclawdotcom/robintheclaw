use crate::lighter_provisioner::PublicLink;
use crate::product::{
    command_transition, ActivityPage, ActivityRecord, AgentCommandRecord, AgentCommandWorkItem,
    AgentReadiness, AgentRecord, AgentSnapshot, ConfirmedVault, ExecutionAccountRecord,
    ExecutionBindingRecord, IdentitySnapshot, MeResponse, OwnerAction, PreferencesInput,
    PreferencesRecord, ReadinessEvidenceInput, SmartAccountRecord, UserRecord, VaultRecord,
    WalletRecord, LIVE_STRATEGY_MANIFEST_SHA256, LIVE_STRATEGY_VERSION,
};
use anyhow::{anyhow, Result};
use chrono::{DateTime, Duration, Utc};
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

pub struct ExecutionBindingConfirmation<'a> {
    pub venue: &'a str,
    pub request_id: Uuid,
    pub owner_address: &'a str,
    pub public_identifier: &'a str,
    pub proof_transaction_hash: &'a str,
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

    pub fn from_pool(pool: PgPool) -> Self {
        Self { pool: Some(pool) }
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

    pub async fn launch_paper_agent(
        &self,
        did: &str,
        strategy_version: &str,
    ) -> Result<AgentRecord> {
        let user = self.ensure_user(did).await?;
        if self.vault_for_user(user.id).await?.is_none() {
            return Err(anyhow!("create a vault before launching an agent"));
        }
        let pool = self.pool()?;
        let record = sqlx::query_as::<_, AgentRecord>(
            r#"
            INSERT INTO agents (id, user_id, strategy_version, mode, status)
            VALUES ($1, $2, $3, 'paper', 'running')
            ON CONFLICT (user_id) DO UPDATE SET
                status = 'running',
                updated_at = now()
            WHERE agents.mode = 'paper'
            RETURNING id, strategy_version, mode, status, created_at, updated_at
            "#,
        )
        .bind(Uuid::new_v4())
        .bind(user.id)
        .bind(strategy_version)
        .fetch_optional(pool)
        .await?;
        record.ok_or_else(|| anyhow!("this account already has a live agent"))
    }

    pub async fn create_live_agent(
        &self,
        did: &str,
        strategy_version: &str,
    ) -> Result<AgentRecord> {
        if strategy_version != LIVE_STRATEGY_VERSION {
            return Err(anyhow!("unsupported live strategy"));
        }
        let user = self.ensure_user(did).await?;
        let pool = self.pool()?;
        let inserted = sqlx::query_as::<_, AgentRecord>(
            r#"
            INSERT INTO agents (id, user_id, strategy_version, mode, status)
            VALUES ($1, $2, $3, 'live', 'setup')
            ON CONFLICT (user_id) DO UPDATE SET
                strategy_version = EXCLUDED.strategy_version,
                mode = 'live',
                status = 'setup',
                blocked_reason = NULL,
                updated_at = now()
            WHERE agents.mode = 'paper'
            RETURNING id, strategy_version, mode, status, created_at, updated_at
            "#,
        )
        .bind(Uuid::new_v4())
        .bind(user.id)
        .bind(strategy_version)
        .fetch_optional(pool)
        .await?;
        if let Some(record) = inserted {
            return Ok(record);
        }
        let existing = sqlx::query_as::<_, AgentRecord>(
            r#"
            SELECT id, strategy_version, mode, status, created_at, updated_at
            FROM agents WHERE user_id = $1
            "#,
        )
        .bind(user.id)
        .fetch_one(pool)
        .await?;
        if existing.mode == "live" && existing.strategy_version == strategy_version {
            Ok(existing)
        } else {
            Err(anyhow!("this account already has a different agent"))
        }
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
            WHERE id = $1 AND user_id = $2 AND mode = 'paper'
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

    pub async fn create_execution_account(
        &self,
        did: &str,
        agent_id: Uuid,
    ) -> Result<ExecutionAccountRecord> {
        let user = self.ensure_user(did).await?;
        let pool = self.pool()?;
        let mut tx = pool.begin().await?;
        let agent = sqlx::query_as::<_, AgentRecord>(
            r#"
            SELECT id, strategy_version, mode, status, created_at, updated_at
            FROM agents WHERE id = $1 AND user_id = $2 FOR UPDATE
            "#,
        )
        .bind(agent_id)
        .bind(user.id)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or_else(|| anyhow!("agent not found"))?;
        if agent.mode != "live" || agent.strategy_version != LIVE_STRATEGY_VERSION {
            return Err(anyhow!(
                "execution accounts are only available for the approved live strategy"
            ));
        }
        let account = sqlx::query_as::<_, ExecutionAccountRecord>(
            r#"
            INSERT INTO execution_accounts (
                id, user_id, agent_id, strategy_version, strategy_manifest_sha256,
                chain_id, status
            ) VALUES ($1, $2, $3, $4, $5, 4663, 'provisioning')
            ON CONFLICT (agent_id) DO UPDATE SET updated_at = execution_accounts.updated_at
            RETURNING id, agent_id, strategy_version, strategy_manifest_sha256,
                chain_id, status, created_at, updated_at
            "#,
        )
        .bind(Uuid::new_v4())
        .bind(user.id)
        .bind(agent_id)
        .bind(LIVE_STRATEGY_VERSION)
        .bind(LIVE_STRATEGY_MANIFEST_SHA256)
        .fetch_one(&mut *tx)
        .await?;
        let inserted = sqlx::query(
            r#"
            INSERT INTO agent_readiness (execution_account_id)
            VALUES ($1) ON CONFLICT (execution_account_id) DO NOTHING
            "#,
        )
        .bind(account.id)
        .execute(&mut *tx)
        .await?;
        if inserted.rows_affected() == 1 {
            let readiness_snapshot_id = Uuid::new_v4();
            for check_name in [
                "lighter_linked",
                "lighter_funded",
                "robinhood_deployed",
                "robinhood_funded",
                "user_gas_ready",
                "execution_gas_ready",
                "policy_active",
                "reconciled",
            ] {
                sqlx::query(
                    r#"
                    INSERT INTO agent_readiness_evidence (
                        id, execution_account_id, snapshot_id, check_name, ready, source,
                        evidence_digest, observed_at, expires_at
                    ) VALUES ($1, $2, $3, $4, false, 'account-bootstrap', $5, now(), now() + interval '1 second')
                    "#,
                )
                .bind(Uuid::new_v4())
                .bind(account.id)
                .bind(readiness_snapshot_id)
                .bind(check_name)
                .bind("0".repeat(64))
                .execute(&mut *tx)
                .await?;
            }
            insert_readiness_snapshot(&mut tx, account.id).await?;
        }
        sqlx::query(
            r#"
            UPDATE agents SET status = 'provisioning', updated_at = now()
            WHERE id = $1 AND status = 'setup'
            "#,
        )
        .bind(agent_id)
        .execute(&mut *tx)
        .await?;
        tx.commit().await?;
        Ok(account)
    }

    pub async fn execution_account(
        &self,
        did: &str,
        agent_id: Uuid,
    ) -> Result<ExecutionAccountRecord> {
        let user = self.ensure_user(did).await?;
        sqlx::query_as::<_, ExecutionAccountRecord>(
            r#"
            SELECT id, agent_id, strategy_version, strategy_manifest_sha256,
                chain_id, status, created_at, updated_at
            FROM execution_accounts WHERE agent_id = $1 AND user_id = $2
            "#,
        )
        .bind(agent_id)
        .bind(user.id)
        .fetch_optional(self.pool()?)
        .await?
        .ok_or_else(|| anyhow!("execution account not found"))
    }

    pub async fn request_execution_binding(
        &self,
        did: &str,
        agent_id: Uuid,
        venue: &str,
        owner_address: &str,
    ) -> Result<ExecutionBindingRecord> {
        if !matches!(venue, "lighter" | "robinhood") {
            return Err(anyhow!("unsupported execution venue"));
        }
        let owner = normalize_address(owner_address)?;
        let user = self.ensure_user(did).await?;
        let pool = self.pool()?;
        let owner_is_linked = sqlx::query_scalar::<_, bool>(
            r#"
            SELECT EXISTS (
                SELECT 1 FROM wallet_links WHERE user_id = $1 AND address = $2
                UNION ALL
                SELECT 1 FROM smart_accounts WHERE user_id = $1 AND address = $2
            )
            "#,
        )
        .bind(user.id)
        .bind(&owner)
        .fetch_one(pool)
        .await?;
        if !owner_is_linked {
            return Err(anyhow!("execution owner is not linked to this account"));
        }
        let account = self.execution_account(did, agent_id).await?;
        let binding = sqlx::query_as::<_, ExecutionBindingRecord>(
            r#"
            INSERT INTO execution_account_bindings (
                id, execution_account_id, venue, binding_ref, request_id,
                owner_address, status
            ) VALUES ($1, $2, $3, $4, $5, $6, 'provisioning')
            ON CONFLICT (execution_account_id, venue) DO UPDATE SET
                updated_at = execution_account_bindings.updated_at
            RETURNING binding_ref, request_id, provider_request_id, venue, owner_address,
                lighter_account_index, lighter_api_key_index, robinhood_vault_address,
                public_identifier, public_key, association_payload, proof_transaction_hash, status,
                created_at, updated_at
            "#,
        )
        .bind(Uuid::new_v4())
        .bind(account.id)
        .bind(venue)
        .bind(Uuid::new_v4())
        .bind(Uuid::new_v4())
        .bind(&owner)
        .fetch_one(pool)
        .await?;
        if binding.owner_address != owner {
            return Err(anyhow!("execution binding owner cannot be changed"));
        }
        sqlx::query(
            "UPDATE agents SET status = 'awaiting_signatures', updated_at = now() WHERE id = $1 AND status IN ('setup', 'provisioning')",
        )
        .bind(agent_id)
        .execute(pool)
        .await?;
        Ok(binding)
    }

    pub async fn confirm_execution_binding(
        &self,
        did: &str,
        agent_id: Uuid,
        confirmation: &ExecutionBindingConfirmation<'_>,
    ) -> Result<ExecutionBindingRecord> {
        let owner = normalize_address(confirmation.owner_address)?;
        let account = self.execution_account(did, agent_id).await?;
        sqlx::query_as::<_, ExecutionBindingRecord>(
            r#"
            UPDATE execution_account_bindings SET
                public_identifier = $5,
                robinhood_vault_address = CASE WHEN $2 = 'robinhood' THEN $5 ELSE robinhood_vault_address END,
                proof_transaction_hash = $6,
                status = 'verifying',
                updated_at = now()
            WHERE execution_account_id = $1 AND venue = $2 AND request_id = $3
              AND owner_address = $4
              AND (
                  status IN ('provisioning', 'awaiting_signature')
                  OR (status = 'verifying' AND public_identifier = $5 AND proof_transaction_hash = $6)
              )
            RETURNING binding_ref, request_id, provider_request_id, venue, owner_address,
                lighter_account_index, lighter_api_key_index, robinhood_vault_address,
                public_identifier, public_key, association_payload, proof_transaction_hash, status,
                created_at, updated_at
            "#,
        )
        .bind(account.id)
        .bind(confirmation.venue)
        .bind(confirmation.request_id)
        .bind(owner)
        .bind(confirmation.public_identifier)
        .bind(confirmation.proof_transaction_hash)
        .fetch_optional(self.pool()?)
        .await?
        .ok_or_else(|| anyhow!("binding request not found or cannot be confirmed"))
    }

    pub async fn apply_lighter_link(
        &self,
        did: &str,
        agent_id: Uuid,
        request_id: Uuid,
        link: &PublicLink,
    ) -> Result<ExecutionBindingRecord> {
        let account = self.execution_account(did, agent_id).await?;
        if account.id != link.execution_account_id {
            return Err(anyhow!(
                "Lighter provisioner returned a different execution account"
            ));
        }
        let owner = normalize_address(&link.owner_address)?;
        if link.account_index <= 0 || !(2..=254).contains(&link.api_key_index) {
            return Err(anyhow!(
                "Lighter provisioner returned an invalid account binding"
            ));
        }
        let status = match link.status.as_str() {
            "generating" | "pending" => "awaiting_signature",
            "verifying" => "verifying",
            "linked" => "linked",
            "superseded" | "blocked" => "rejected",
            _ => return Err(anyhow!("Lighter provisioner returned an unknown status")),
        };
        if matches!(status, "awaiting_signature" | "verifying" | "linked")
            && link.public_key.as_deref().is_none_or(str::is_empty)
        {
            return Err(anyhow!("Lighter provisioner omitted the public key"));
        }
        let public_identifier =
            format!("account:{}:key:{}", link.account_index, link.api_key_index);
        sqlx::query_as::<_, ExecutionBindingRecord>(
            r#"
            UPDATE execution_account_bindings SET
                provider_request_id = $4,
                lighter_account_index = $5,
                lighter_api_key_index = $6,
                public_identifier = $7,
                public_key = $8,
                association_payload = coalesce($9, association_payload),
                proof_transaction_hash = coalesce($10, proof_transaction_hash),
                status = $11,
                updated_at = now()
            WHERE execution_account_id = $1 AND venue = 'lighter' AND request_id = $2
              AND owner_address = $3
              AND (provider_request_id IS NULL OR provider_request_id = $4)
              AND (lighter_account_index IS NULL OR lighter_account_index = $5)
              AND (lighter_api_key_index IS NULL OR lighter_api_key_index = $6)
              AND (public_key IS NULL OR lower(public_key) = lower($8))
            RETURNING binding_ref, request_id, provider_request_id, venue, owner_address,
                lighter_account_index, lighter_api_key_index, robinhood_vault_address,
                public_identifier, public_key, association_payload, proof_transaction_hash, status,
                created_at, updated_at
            "#,
        )
        .bind(account.id)
        .bind(request_id)
        .bind(owner)
        .bind(link.link_id)
        .bind(link.account_index)
        .bind(i16::from(link.api_key_index))
        .bind(public_identifier)
        .bind(link.public_key.as_deref())
        .bind(link.message_to_sign.as_deref())
        .bind(link.transaction_hash.as_deref())
        .bind(status)
        .fetch_optional(self.pool()?)
        .await?
        .ok_or_else(|| anyhow!("Lighter binding does not match its provisioned account"))
    }

    pub async fn execution_binding(
        &self,
        did: &str,
        agent_id: Uuid,
        venue: &str,
        request_id: Uuid,
    ) -> Result<ExecutionBindingRecord> {
        let account = self.execution_account(did, agent_id).await?;
        sqlx::query_as::<_, ExecutionBindingRecord>(
            r#"
            SELECT binding_ref, request_id, provider_request_id, venue, owner_address,
                lighter_account_index, lighter_api_key_index, robinhood_vault_address,
                public_identifier, public_key, association_payload, proof_transaction_hash, status,
                created_at, updated_at
            FROM execution_account_bindings
            WHERE execution_account_id = $1 AND venue = $2 AND request_id = $3
            "#,
        )
        .bind(account.id)
        .bind(venue)
        .bind(request_id)
        .fetch_optional(self.pool()?)
        .await?
        .ok_or_else(|| anyhow!("execution binding not found"))
    }

    pub async fn agent_readiness(&self, did: &str, agent_id: Uuid) -> Result<AgentReadiness> {
        let user = self.ensure_user(did).await?;
        let readiness = sqlx::query_as::<_, AgentReadiness>(
            r#"
            SELECT r.execution_account_id, r.lighter_linked, r.lighter_funded,
                r.robinhood_deployed, r.robinhood_funded, r.user_gas_ready,
                r.execution_gas_ready, r.policy_active, r.reconciled, r.valid_until
            FROM current_agent_readiness r
            JOIN execution_accounts e ON e.id = r.execution_account_id
            WHERE e.agent_id = $1 AND e.user_id = $2
            "#,
        )
        .bind(agent_id)
        .bind(user.id)
        .fetch_optional(self.pool()?)
        .await?
        .ok_or_else(|| anyhow!("agent readiness is not available"))?;
        Ok(readiness.finalize())
    }

    pub async fn create_agent_command(
        &self,
        did: &str,
        agent_id: Uuid,
        idempotency_key: &str,
        command: &str,
    ) -> Result<AgentCommandRecord> {
        let user = self.ensure_user(did).await?;
        let pool = self.pool()?;
        let mut tx = pool.begin().await?;
        let (agent_status, account_id) = sqlx::query_as::<_, (String, Uuid)>(
            r#"
            SELECT a.status, e.id
            FROM agents a JOIN execution_accounts e ON e.agent_id = a.id
            WHERE a.id = $1 AND a.user_id = $2 AND a.mode = 'live'
            FOR UPDATE OF a
            "#,
        )
        .bind(agent_id)
        .bind(user.id)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or_else(|| anyhow!("live agent not found"))?;
        if let Some(existing) = sqlx::query_as::<_, AgentCommandRecord>(
            r#"
            SELECT id, agent_id, execution_account_id, idempotency_key,
                command, status, agent_status, target_agent_status, error_reason,
                result_evidence_digest, result_owner_actions AS owner_actions,
                completed_at, created_at, updated_at
            FROM agent_commands
            WHERE agent_id = $1 AND idempotency_key = $2
            "#,
        )
        .bind(agent_id)
        .bind(idempotency_key)
        .fetch_optional(&mut *tx)
        .await?
        {
            if existing.command != command {
                return Err(anyhow!(
                    "idempotency key was reused for a different command"
                ));
            }
            tx.commit().await?;
            return Ok(existing);
        }
        let readiness = sqlx::query_as::<_, AgentReadiness>(
            r#"
            SELECT execution_account_id, lighter_linked, lighter_funded,
                robinhood_deployed, robinhood_funded, user_gas_ready,
                execution_gas_ready, policy_active, reconciled, valid_until
            FROM current_agent_readiness WHERE execution_account_id = $1
            "#,
        )
        .bind(account_id)
        .fetch_one(&mut *tx)
        .await?
        .finalize();
        let transition = command_transition(
            &agent_status,
            command,
            readiness.can_launch,
            readiness.reconciled,
        );
        let (command_status, next_status, error_reason) = match transition {
            Ok(next) => ("pending", next, None),
            Err(reason) => ("rejected", agent_status.as_str(), Some(reason)),
        };
        let command_id = Uuid::new_v4();
        let record = sqlx::query_as::<_, AgentCommandRecord>(
            r#"
            INSERT INTO agent_commands (
                id, agent_id, execution_account_id, idempotency_key, command,
                status, agent_status, target_agent_status, error_reason
            ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
            RETURNING id, agent_id, execution_account_id, idempotency_key,
                command, status, agent_status, target_agent_status, error_reason,
                result_evidence_digest, result_owner_actions AS owner_actions,
                completed_at, created_at, updated_at
            "#,
        )
        .bind(command_id)
        .bind(agent_id)
        .bind(account_id)
        .bind(idempotency_key)
        .bind(command)
        .bind(command_status)
        .bind(&agent_status)
        .bind(next_status)
        .bind(error_reason)
        .fetch_one(&mut *tx)
        .await?;
        if command_status == "pending" {
            sqlx::query("INSERT INTO agent_command_outbox (command_id) VALUES ($1)")
                .bind(command_id)
                .execute(&mut *tx)
                .await?;
        }
        tx.commit().await?;
        Ok(record)
    }

    pub async fn agent_command(
        &self,
        did: &str,
        agent_id: Uuid,
        command_id: Uuid,
    ) -> Result<AgentCommandRecord> {
        let user = self.ensure_user(did).await?;
        sqlx::query_as::<_, AgentCommandRecord>(
            r#"
            SELECT c.id, c.agent_id, c.execution_account_id, c.idempotency_key,
                c.command, c.status, c.agent_status, c.target_agent_status,
                c.error_reason, c.result_evidence_digest,
                c.result_owner_actions AS owner_actions, c.completed_at,
                c.created_at, c.updated_at
            FROM agent_commands c JOIN agents a ON a.id = c.agent_id
            WHERE c.id = $1 AND c.agent_id = $2 AND a.user_id = $3
            "#,
        )
        .bind(command_id)
        .bind(agent_id)
        .bind(user.id)
        .fetch_optional(self.pool()?)
        .await?
        .ok_or_else(|| anyhow!("agent command not found"))
    }

    pub async fn record_readiness_snapshot(
        &self,
        execution_account_id: Uuid,
        evidence: &[ReadinessEvidenceInput<'_>],
    ) -> Result<AgentReadiness> {
        const CHECKS: [&str; 8] = [
            "execution_gas_ready",
            "lighter_funded",
            "lighter_linked",
            "policy_active",
            "reconciled",
            "robinhood_deployed",
            "robinhood_funded",
            "user_gas_ready",
        ];

        let mut provided: Vec<&str> = evidence.iter().map(|item| item.check_name).collect();
        provided.sort_unstable();
        if provided != CHECKS {
            return Err(anyhow!(
                "readiness snapshot must contain every check exactly once"
            ));
        }
        for item in evidence {
            if item.source.trim().is_empty() || item.source.len() > 128 {
                return Err(anyhow!("invalid readiness evidence source"));
            }
            if item.evidence_digest.len() != 64
                || !item
                    .evidence_digest
                    .bytes()
                    .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
            {
                return Err(anyhow!("invalid readiness evidence digest"));
            }
            let max_age = if matches!(item.check_name, "lighter_linked" | "robinhood_deployed") {
                Duration::hours(24)
            } else {
                Duration::seconds(60)
            };
            if item.expires_at <= item.observed_at || item.expires_at - item.observed_at > max_age {
                return Err(anyhow!("invalid readiness evidence lifetime"));
            }
            let freshness = if matches!(item.check_name, "lighter_linked" | "robinhood_deployed") {
                Duration::hours(24)
            } else {
                Duration::seconds(5)
            };
            let now = Utc::now();
            if item.ready
                && (item.observed_at < now - freshness
                    || item.observed_at > now + Duration::seconds(5)
                    || item.expires_at <= now)
            {
                return Err(anyhow!("readiness evidence is stale or future-dated"));
            }
        }

        let pool = self.pool()?;
        let mut tx = pool.begin().await?;
        let exists = sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS (SELECT 1 FROM execution_accounts WHERE id = $1)",
        )
        .bind(execution_account_id)
        .fetch_one(&mut *tx)
        .await?;
        if !exists {
            return Err(anyhow!("execution account not found"));
        }

        let snapshot_id = Uuid::new_v4();
        for item in evidence {
            sqlx::query(
                r#"
                INSERT INTO agent_readiness_evidence (
                    id, execution_account_id, snapshot_id, check_name, ready, source,
                    evidence_digest, observed_at, expires_at
                ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
                "#,
            )
            .bind(Uuid::new_v4())
            .bind(execution_account_id)
            .bind(snapshot_id)
            .bind(item.check_name)
            .bind(item.ready)
            .bind(item.source)
            .bind(item.evidence_digest)
            .bind(item.observed_at)
            .bind(item.expires_at)
            .execute(&mut *tx)
            .await?;
        }
        insert_readiness_snapshot(&mut tx, execution_account_id).await?;
        let readiness = sqlx::query_as::<_, AgentReadiness>(
            r#"
            SELECT execution_account_id, lighter_linked, lighter_funded,
                robinhood_deployed, robinhood_funded, user_gas_ready,
                execution_gas_ready, policy_active, reconciled, valid_until
            FROM current_agent_readiness WHERE execution_account_id = $1
            "#,
        )
        .bind(execution_account_id)
        .fetch_one(&mut *tx)
        .await?
        .finalize();
        let lifecycle_status = if readiness.can_launch {
            "ready"
        } else if !readiness.lighter_linked || !readiness.robinhood_deployed {
            "awaiting_signatures"
        } else {
            "awaiting_funding"
        };
        sqlx::query(
            r#"
            UPDATE execution_accounts SET status = $2, updated_at = now()
            WHERE id = $1 AND status IN (
                'provisioning', 'awaiting_signatures', 'awaiting_funding', 'ready'
            )
            "#,
        )
        .bind(execution_account_id)
        .bind(lifecycle_status)
        .execute(&mut *tx)
        .await?;
        sqlx::query(
            r#"
            UPDATE agents agent SET status = $2, blocked_reason = NULL, updated_at = now()
            FROM execution_accounts account
            WHERE account.id = $1 AND agent.id = account.agent_id
              AND agent.status IN (
                  'provisioning', 'awaiting_signatures', 'awaiting_funding', 'ready'
              )
            "#,
        )
        .bind(execution_account_id)
        .bind(lifecycle_status)
        .execute(&mut *tx)
        .await?;
        tx.commit().await?;
        Ok(readiness)
    }

    pub async fn claim_internal_nonce(
        &self,
        scope: &str,
        caller: &str,
        nonce: &str,
        expires_at: DateTime<Utc>,
    ) -> Result<bool> {
        let pool = self.pool()?;
        let mut tx = pool.begin().await?;
        sqlx::query("DELETE FROM app_internal_nonces WHERE expires_at < now()")
            .execute(&mut *tx)
            .await?;
        let inserted = sqlx::query(
            r#"
            INSERT INTO app_internal_nonces (scope, caller, nonce, expires_at)
            VALUES ($1, $2, $3, $4)
            ON CONFLICT DO NOTHING
            "#,
        )
        .bind(scope)
        .bind(caller)
        .bind(nonce)
        .bind(expires_at)
        .execute(&mut *tx)
        .await?;
        tx.commit().await?;
        Ok(inserted.rows_affected() == 1)
    }

    pub async fn claim_agent_commands(
        &self,
        worker_id: &str,
        limit: u32,
    ) -> Result<Vec<AgentCommandWorkItem>> {
        if worker_id.trim().is_empty() || worker_id.len() > 128 || limit == 0 || limit > 100 {
            return Err(anyhow!("invalid command worker claim"));
        }
        sqlx::query_as::<_, AgentCommandWorkItem>(
            r#"
            WITH selected AS (
                SELECT outbox.command_id
                FROM agent_command_outbox outbox
                JOIN agent_commands command ON command.id = outbox.command_id
                WHERE outbox.delivered_at IS NULL
                  AND outbox.claimed_at IS NULL
                  AND outbox.available_at <= now()
                  AND command.status = 'pending'
                ORDER BY outbox.available_at, outbox.command_id
                LIMIT $2
                FOR UPDATE OF outbox SKIP LOCKED
            ), claimed AS (
                UPDATE agent_command_outbox outbox SET
                    claimed_at = now(), claimed_by = $1, attempts = attempts + 1
                FROM selected
                WHERE outbox.command_id = selected.command_id
                RETURNING outbox.command_id
            )
            UPDATE agent_commands command SET status = 'processing',
                dispatch_requested_at = coalesce(command.dispatch_requested_at, clock_timestamp()),
                updated_at = now()
            FROM claimed
            WHERE command.id = claimed.command_id
            RETURNING command.id, command.agent_id, command.execution_account_id,
                command.command, command.agent_status, command.target_agent_status,
                (EXTRACT(EPOCH FROM command.dispatch_requested_at) * 1000)::bigint
                    AS requested_at_ms
            "#,
        )
        .bind(worker_id)
        .bind(i64::from(limit))
        .fetch_all(self.pool()?)
        .await
        .map_err(Into::into)
    }

    pub async fn recover_agent_commands(&self, limit: u32) -> Result<Vec<AgentCommandWorkItem>> {
        if limit == 0 || limit > 100 {
            return Err(anyhow!("invalid command recovery limit"));
        }
        sqlx::query_as::<_, AgentCommandWorkItem>(
            r#"
            SELECT command.id, command.agent_id, command.execution_account_id,
                command.command, command.agent_status, command.target_agent_status,
                (EXTRACT(EPOCH FROM command.dispatch_requested_at) * 1000)::bigint
                    AS requested_at_ms
            FROM agent_commands command
            JOIN agent_command_outbox outbox ON outbox.command_id = command.id
            WHERE command.status IN ('processing', 'awaiting_signature')
              AND command.dispatch_requested_at IS NOT NULL
              AND outbox.delivered_at IS NULL
            ORDER BY command.updated_at, command.id
            LIMIT $1
            "#,
        )
        .bind(i64::from(limit))
        .fetch_all(self.pool()?)
        .await
        .map_err(Into::into)
    }

    pub async fn await_agent_command_signature(
        &self,
        command_id: Uuid,
        evidence_digest: &str,
        owner_actions: &[OwnerAction],
    ) -> Result<()> {
        validate_evidence_digest(evidence_digest)?;
        if owner_actions.is_empty() {
            return Err(anyhow!("owner signature command omitted its actions"));
        }
        let updated = sqlx::query(
            r#"
            UPDATE agent_commands SET status = 'awaiting_signature',
                result_evidence_digest = $2, result_owner_actions = $3,
                updated_at = now()
            WHERE id = $1 AND command = 'withdraw'
              AND status IN ('processing', 'awaiting_signature')
            "#,
        )
        .bind(command_id)
        .bind(evidence_digest)
        .bind(sqlx::types::Json(owner_actions))
        .execute(self.pool()?)
        .await?;
        if updated.rows_affected() != 1 {
            return Err(anyhow!("agent command is not awaiting an owner signature"));
        }
        Ok(())
    }

    pub async fn complete_reconciled_agent_command(
        &self,
        command_id: Uuid,
        evidence_digest: &str,
        error_reason: Option<&str>,
    ) -> Result<AgentCommandRecord> {
        validate_evidence_digest(evidence_digest)?;
        if error_reason.is_some_and(|reason| reason.trim().is_empty()) {
            return Err(anyhow!("invalid command failure reason"));
        }

        let pool = self.pool()?;
        let mut tx = pool.begin().await?;
        let (status, agent_id, initial_status, target_status, current_status) =
            sqlx::query_as::<_, (String, Uuid, String, String, String)>(
                r#"
                SELECT command.status, command.agent_id, command.agent_status,
                    command.target_agent_status, agent.status
                FROM agent_commands command
                JOIN agents agent ON agent.id = command.agent_id
                JOIN agent_command_outbox outbox ON outbox.command_id = command.id
                WHERE command.id = $1
                FOR UPDATE OF command, agent, outbox
                "#,
            )
            .bind(command_id)
            .fetch_optional(&mut *tx)
            .await?
            .ok_or_else(|| anyhow!("agent command not found"))?;
        if !matches!(status.as_str(), "processing" | "awaiting_signature") {
            return Err(anyhow!("agent command is not awaiting completion"));
        }

        let final_status = if let Some(reason) = error_reason {
            sqlx::query(
                r#"
                UPDATE agent_commands SET status = 'failed', error_reason = $2,
                    result_evidence_digest = $3, result_owner_actions = '[]'::jsonb,
                    completed_at = now(), updated_at = now()
                WHERE id = $1
                "#,
            )
            .bind(command_id)
            .bind(reason)
            .bind(evidence_digest)
            .execute(&mut *tx)
            .await?;
            "failed"
        } else {
            if current_status != initial_status {
                return Err(anyhow!("agent state changed while command was in flight"));
            }
            sqlx::query(
                "UPDATE agents SET status = $2, blocked_reason = NULL, updated_at = now() WHERE id = $1",
            )
            .bind(agent_id)
            .bind(&target_status)
            .execute(&mut *tx)
            .await?;
            sqlx::query(
                r#"
                UPDATE agent_commands SET status = 'completed', agent_status = $2,
                    result_evidence_digest = $3, result_owner_actions = '[]'::jsonb,
                    completed_at = now(), updated_at = now()
                WHERE id = $1
                "#,
            )
            .bind(command_id)
            .bind(&target_status)
            .bind(evidence_digest)
            .execute(&mut *tx)
            .await?;
            "completed"
        };
        sqlx::query(
            r#"
            UPDATE agent_command_outbox SET delivered_at = now(),
                last_error = $2 WHERE command_id = $1
            "#,
        )
        .bind(command_id)
        .bind(error_reason)
        .execute(&mut *tx)
        .await?;
        let record = sqlx::query_as::<_, AgentCommandRecord>(
            r#"
            SELECT id, agent_id, execution_account_id, idempotency_key,
                command, status, agent_status, target_agent_status, error_reason,
                result_evidence_digest, result_owner_actions AS owner_actions,
                completed_at, created_at, updated_at
            FROM agent_commands WHERE id = $1 AND status = $2
            "#,
        )
        .bind(command_id)
        .bind(final_status)
        .fetch_one(&mut *tx)
        .await?;
        tx.commit().await?;
        Ok(record)
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

fn validate_evidence_digest(value: &str) -> Result<()> {
    if value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
    {
        return Ok(());
    }
    Err(anyhow!("invalid command evidence digest"))
}

async fn insert_readiness_snapshot(
    tx: &mut Transaction<'_, Postgres>,
    execution_account_id: Uuid,
) -> Result<()> {
    sqlx::query(
        r#"
        INSERT INTO agent_readiness_snapshots (
            id, execution_account_id, lighter_linked, lighter_funded,
            robinhood_deployed, robinhood_funded, user_gas_ready,
            execution_gas_ready, policy_active, reconciled, observed_at
        )
        SELECT $1, execution_account_id, lighter_linked, lighter_funded,
            robinhood_deployed, robinhood_funded, user_gas_ready,
            execution_gas_ready, policy_active, reconciled, now()
        FROM current_agent_readiness WHERE execution_account_id = $2
        "#,
    )
    .bind(Uuid::new_v4())
    .bind(execution_account_id)
    .execute(&mut **tx)
    .await?;
    Ok(())
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
