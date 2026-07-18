use app::account_registration::AccountRegistration;
use app::product::OwnerAction;
use app::product_store::ProductStore;
use app::robinhood_provisioner::{Graph, PublicGraphBinding, UnsignedAction};
use chrono::{Duration, Utc};
use sha3::{Digest, Keccak256};
use sqlx::postgres::PgPoolOptions;
use uuid::Uuid;

fn random_address() -> String {
    let value = Uuid::new_v4().simple().to_string();
    format!("0x{value}{}", &value[..8])
}

fn deploy_data(owner: &str) -> String {
    let selector = Keccak256::digest(b"deploy(address)");
    format!(
        "0x{}{}{}",
        hex::encode(&selector[..4]),
        "0".repeat(24),
        owner.trim_start_matches("0x").to_ascii_lowercase()
    )
}

fn authorize_data(signer: &str) -> String {
    let selector = Keccak256::digest(b"authorizeInitialAgent(address)");
    format!(
        "0x{}{}{}",
        hex::encode(&selector[..4]),
        "0".repeat(24),
        signer.trim_start_matches("0x").to_ascii_lowercase()
    )
}

fn random_hash() -> String {
    format!("0x{}{}", Uuid::new_v4().simple(), Uuid::new_v4().simple())
}

async fn insert_provisioning_agent(pool: &sqlx::PgPool) -> (String, Uuid, Uuid) {
    let user_id = Uuid::new_v4();
    let agent_id = Uuid::new_v4();
    let account_id = Uuid::new_v4();
    let did = format!("did:test:{user_id}");
    sqlx::query("INSERT INTO users (id, privy_did) VALUES ($1, $2)")
        .bind(user_id)
        .bind(&did)
        .execute(pool)
        .await
        .unwrap();
    sqlx::query(
        "INSERT INTO agents (id, user_id, strategy_version, mode, status) VALUES ($1, $2, 'basis-aapl-v1', 'live', 'provisioning')",
    )
    .bind(agent_id)
    .bind(user_id)
    .execute(pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO execution_accounts (id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status) VALUES ($1, $2, $3, 'basis-aapl-v1', 'c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32', 'provisioning')",
    )
    .bind(account_id)
    .bind(user_id)
    .bind(agent_id)
    .execute(pool)
    .await
    .unwrap();
    (did, agent_id, account_id)
}

async fn insert_release_blocked_account(
    pool: &sqlx::PgPool,
) -> (String, Uuid, Uuid, AccountRegistration) {
    let user_id = Uuid::new_v4();
    let agent_id = Uuid::new_v4();
    let account_id = Uuid::new_v4();
    let did = format!("did:test:{user_id}");
    sqlx::query("INSERT INTO users (id, privy_did) VALUES ($1, $2)")
        .bind(user_id)
        .bind(&did)
        .execute(pool)
        .await
        .unwrap();
    sqlx::query(
        r#"
        INSERT INTO agents (
            id, user_id, strategy_version, mode, status, blocked_reason
        ) VALUES (
            $1, $2, 'basis-aapl-v1', 'live', 'blocked',
            'strategy release changed; reconcile before reprovisioning'
        )
        "#,
    )
    .bind(agent_id)
    .bind(user_id)
    .execute(pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
        INSERT INTO execution_accounts (
            id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status
        ) VALUES (
            $1, $2, $3, 'basis-aapl-v1',
            'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f',
            'blocked'
        )
        "#,
    )
    .bind(account_id)
    .bind(user_id)
    .bind(agent_id)
    .execute(pool)
    .await
    .unwrap();

    let lighter_account_index = i64::from(u32::from_be_bytes(
        account_id.as_bytes()[..4].try_into().unwrap(),
    )) + 1;
    let mut registration = AccountRegistration {
        execution_account_id: account_id,
        agent_id,
        strategy_version: "basis-aapl-v1".into(),
        risk_version: "basis-aapl-v1".into(),
        strategy_manifest_sha256:
            "da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f".into(),
        lighter_account_index,
        lighter_api_key_index: 254,
        robinhood_owner: random_address().to_ascii_lowercase(),
        robinhood_vault: random_address().to_ascii_lowercase(),
        robinhood_signer: random_address().to_ascii_lowercase(),
        binding_sha256: String::new(),
    };
    registration.binding_sha256 = registration.calculate_binding_sha256();
    sqlx::query(
        r#"
        INSERT INTO coordinator_account_registrations (
            execution_account_id, agent_id, strategy_version, risk_version,
            strategy_manifest_sha256, lighter_account_index, lighter_api_key_index,
            robinhood_owner, robinhood_vault, robinhood_signer, binding_sha256,
            status, coordinator_account_status, coordinator_control_mode,
            registered_at, last_error
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
            'blocked', 'blocked', 'HALTED', now(),
            'strategy release changed; registration must not be reused'
        )
        "#,
    )
    .bind(registration.execution_account_id)
    .bind(registration.agent_id)
    .bind(&registration.strategy_version)
    .bind(&registration.risk_version)
    .bind(&registration.strategy_manifest_sha256)
    .bind(registration.lighter_account_index)
    .bind(registration.lighter_api_key_index)
    .bind(&registration.robinhood_owner)
    .bind(&registration.robinhood_vault)
    .bind(&registration.robinhood_signer)
    .bind(&registration.binding_sha256)
    .execute(pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
        INSERT INTO execution_account_bindings (
            id, execution_account_id, venue, binding_ref, request_id, owner_address,
            lighter_account_index, lighter_api_key_index, public_identifier, status
        ) VALUES ($1, $2, 'lighter', $3, $4, $5, $6, $7, $8, 'linked')
        "#,
    )
    .bind(Uuid::new_v4())
    .bind(account_id)
    .bind(Uuid::new_v4())
    .bind(Uuid::new_v4())
    .bind(&registration.robinhood_owner)
    .bind(registration.lighter_account_index)
    .bind(registration.lighter_api_key_index)
    .bind(format!(
        "account:{}:key:{}",
        registration.lighter_account_index, registration.lighter_api_key_index
    ))
    .execute(pool)
    .await
    .unwrap();
    (did, agent_id, account_id, registration)
}

#[tokio::test]
#[ignore = "requires APP_TEST_DATABASE_URL"]
async fn release_upgrade_terminalizes_registration_outbox() {
    let database_url = std::env::var("APP_TEST_DATABASE_URL").expect("APP_TEST_DATABASE_URL");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database_url)
        .await
        .unwrap();
    sqlx::raw_sql(
        "DROP SCHEMA IF EXISTS release_upgrade_outbox CASCADE; \
         CREATE SCHEMA release_upgrade_outbox",
    )
    .execute(&pool)
    .await
    .unwrap();
    let mut connection = pool.acquire().await.unwrap();
    sqlx::raw_sql("SET search_path TO release_upgrade_outbox")
        .execute(&mut *connection)
        .await
        .unwrap();

    for migration in [
        include_str!("../migrations/0001_product.sql"),
        include_str!("../migrations/0002_agents.sql"),
        include_str!("../migrations/0003_mainnet_agents.sql"),
        include_str!("../migrations/0004_live_agent_hardening.sql"),
        include_str!("../migrations/0005_command_dispatch.sql"),
        include_str!("../migrations/0006_robinhood_provisioning.sql"),
        include_str!("../migrations/0007_account_registration.sql"),
        include_str!("../migrations/0008_robinhood_authorization_proof.sql"),
        include_str!("../migrations/0009_repin_strategy_manifest.sql"),
    ] {
        sqlx::raw_sql(migration)
            .execute(&mut *connection)
            .await
            .unwrap();
    }

    let user_id = Uuid::new_v4();
    let agent_id = Uuid::new_v4();
    let account_id = Uuid::new_v4();
    sqlx::query("INSERT INTO users (id, privy_did) VALUES ($1, $2)")
        .bind(user_id)
        .bind(format!("did:test:{user_id}"))
        .execute(&mut *connection)
        .await
        .unwrap();
    sqlx::query(
        "INSERT INTO agents (id, user_id, strategy_version, mode, status) \
         VALUES ($1, $2, 'basis-aapl-v1', 'live', 'ready')",
    )
    .bind(agent_id)
    .bind(user_id)
    .execute(&mut *connection)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO execution_accounts \
         (id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status) \
         VALUES ($1, $2, $3, 'basis-aapl-v1', $4, 'ready')",
    )
    .bind(account_id)
    .bind(user_id)
    .bind(agent_id)
    .bind("da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f")
    .execute(&mut *connection)
    .await
    .unwrap();
    sqlx::query(
        r#"
        INSERT INTO coordinator_account_registrations (
            execution_account_id, agent_id, strategy_version, risk_version,
            strategy_manifest_sha256, lighter_account_index, lighter_api_key_index,
            robinhood_owner, robinhood_vault, robinhood_signer, binding_sha256,
            status, registered_at
        ) VALUES (
            $1, $2, 'basis-aapl-v1', 'basis-aapl-v1', $3, 91, 254,
            '0x0000000000000000000000000000000000000091',
            '0x0000000000000000000000000000000000000092',
            '0x0000000000000000000000000000000000000093',
            $4, 'registered', now()
        )
        "#,
    )
    .bind(account_id)
    .bind(agent_id)
    .bind("da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f")
    .bind("a".repeat(64))
    .execute(&mut *connection)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO coordinator_account_registration_outbox \
         (execution_account_id, claimed_at, claimed_by) VALUES ($1, now(), 'old-worker')",
    )
    .bind(account_id)
    .execute(&mut *connection)
    .await
    .unwrap();

    sqlx::raw_sql(include_str!(
        "../migrations/0010_repin_private_strategy_policy.sql"
    ))
    .execute(&mut *connection)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!(
        "../migrations/0011_execution_account_generations.sql"
    ))
    .execute(&mut *connection)
    .await
    .unwrap();

    let state = sqlx::query_as::<_, (String, Option<String>, bool, bool, bool)>(
        r#"
        SELECT registration.status, registration.last_error,
               outbox.delivered_at IS NOT NULL,
               outbox.claimed_at IS NULL,
               outbox.claimed_by IS NULL
        FROM coordinator_account_registrations registration
        JOIN coordinator_account_registration_outbox outbox USING (execution_account_id)
        WHERE registration.execution_account_id = $1
        "#,
    )
    .bind(account_id)
    .fetch_one(&mut *connection)
    .await
    .unwrap();
    assert_eq!(
        state,
        (
            "blocked".into(),
            Some("strategy release changed; registration must not be reused".into()),
            true,
            true,
            true,
        )
    );

    let legacy_upsert = sqlx::query(
        r#"
        INSERT INTO agents (id, user_id, strategy_version, mode, status)
        VALUES ($1, $2, 'basis-aapl-v1', 'live', 'setup')
        ON CONFLICT (user_id) DO UPDATE SET
            strategy_version = EXCLUDED.strategy_version,
            mode = 'live',
            status = 'setup',
            blocked_reason = NULL,
            updated_at = now()
        WHERE agents.mode = 'paper'
        "#,
    )
    .bind(Uuid::new_v4())
    .bind(user_id)
    .execute(&mut *connection)
    .await
    .unwrap();
    assert_eq!(legacy_upsert.rows_affected(), 0);
}

#[tokio::test]
#[ignore = "requires APP_TEST_DATABASE_URL"]
async fn readiness_is_complete_fresh_append_only_and_tenant_unique() {
    let database_url = std::env::var("APP_TEST_DATABASE_URL").expect("APP_TEST_DATABASE_URL");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database_url)
        .await
        .unwrap();
    sqlx::migrate!().run(&pool).await.unwrap();
    let user_id = Uuid::new_v4();
    let agent_id = Uuid::new_v4();
    let account_id = Uuid::new_v4();
    let did = format!("did:test:{user_id}");
    let account_index = i64::from(u32::from_be_bytes(
        account_id.as_bytes()[..4].try_into().unwrap(),
    )) + 1;
    sqlx::query("INSERT INTO users (id, privy_did) VALUES ($1, $2)")
        .bind(user_id)
        .bind(&did)
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query(
        "INSERT INTO agents (id, user_id, strategy_version, mode, status) VALUES ($1, $2, 'basis-aapl-v1', 'live', 'provisioning')",
    )
    .bind(agent_id)
    .bind(user_id)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO execution_accounts (id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status) VALUES ($1, $2, $3, 'basis-aapl-v1', 'c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32', 'provisioning')",
    )
    .bind(account_id)
    .bind(user_id)
    .bind(agent_id)
    .execute(&pool)
    .await
    .unwrap();

    let snapshot_id = Uuid::new_v4();
    let observed_at = Utc::now();
    let mut evidence_id = Uuid::nil();
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
        evidence_id = Uuid::new_v4();
        let expires_at = observed_at
            + if matches!(check_name, "lighter_linked" | "robinhood_deployed") {
                Duration::hours(1)
            } else {
                Duration::seconds(30)
            };
        sqlx::query(
            r#"
            INSERT INTO agent_readiness_evidence (
                id, execution_account_id, snapshot_id, check_name, ready, source,
                evidence_digest, observed_at, expires_at
            ) VALUES ($1, $2, $3, $4, true, 'integration-test', $5, $6, $7)
            "#,
        )
        .bind(evidence_id)
        .bind(account_id)
        .bind(snapshot_id)
        .bind(check_name)
        .bind("1".repeat(64))
        .bind(observed_at)
        .bind(expires_at)
        .execute(&pool)
        .await
        .unwrap();
    }
    let readiness = sqlx::query_as::<_, (bool, bool, bool, bool, bool, bool, bool, bool)>(
        r#"
        SELECT lighter_linked, lighter_funded, robinhood_deployed, robinhood_funded,
            user_gas_ready, execution_gas_ready, policy_active, reconciled
        FROM current_agent_readiness WHERE execution_account_id = $1
        "#,
    )
    .bind(account_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(readiness, (true, true, true, true, true, true, true, true));
    let store = ProductStore::from_pool(pool.clone());
    let unregistered = store.agent_readiness(&did, agent_id).await.unwrap();
    assert!(!unregistered.can_launch);
    assert!(unregistered
        .blockers
        .iter()
        .any(|blocker| blocker == "coordinator_account_not_registered"));
    let rejected = store
        .create_agent_command(&did, agent_id, "launch-without-registration", "launch")
        .await
        .unwrap();
    assert_eq!(rejected.status, "rejected");
    assert_eq!(
        rejected.error_reason.as_deref(),
        Some("coordinator_account_not_registered")
    );

    let mut registration = AccountRegistration {
        execution_account_id: account_id,
        agent_id,
        strategy_version: "basis-aapl-v1".into(),
        risk_version: "basis-aapl-v1".into(),
        strategy_manifest_sha256:
            "c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32".into(),
        lighter_account_index: account_index,
        lighter_api_key_index: 254,
        robinhood_owner: random_address().to_ascii_lowercase(),
        robinhood_vault: random_address().to_ascii_lowercase(),
        robinhood_signer: random_address().to_ascii_lowercase(),
        binding_sha256: String::new(),
    };
    registration.binding_sha256 = registration.calculate_binding_sha256();
    sqlx::query(
        r#"
        INSERT INTO coordinator_account_registrations (
            execution_account_id, agent_id, strategy_version, risk_version,
            strategy_manifest_sha256, lighter_account_index, lighter_api_key_index,
            robinhood_owner, robinhood_vault, robinhood_signer, binding_sha256,
            status, coordinator_account_status, coordinator_control_mode, registered_at
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
                  'registered', 'active', 'ACTIVE', now())
        "#,
    )
    .bind(registration.execution_account_id)
    .bind(registration.agent_id)
    .bind(&registration.strategy_version)
    .bind(&registration.risk_version)
    .bind(&registration.strategy_manifest_sha256)
    .bind(registration.lighter_account_index)
    .bind(registration.lighter_api_key_index)
    .bind(&registration.robinhood_owner)
    .bind(&registration.robinhood_vault)
    .bind(&registration.robinhood_signer)
    .bind(&registration.binding_sha256)
    .execute(&pool)
    .await
    .unwrap();
    let registered = store.agent_readiness(&did, agent_id).await.unwrap();
    assert!(registered.coordinator_registered);
    assert!(registered.can_launch);

    sqlx::query("UPDATE agents SET status = 'running' WHERE id = $1")
        .bind(agent_id)
        .execute(&pool)
        .await
        .unwrap();
    let pause = store
        .create_agent_command(&did, agent_id, "pause-integration", "pause")
        .await
        .unwrap();
    assert_eq!(pause.status, "pending");
    assert_eq!(pause.agent_status, "reducing");
    assert_eq!(pause.target_agent_status, "paused");
    let reducing = sqlx::query_scalar::<_, String>("SELECT status FROM agents WHERE id = $1")
        .bind(agent_id)
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(reducing, "reducing");
    assert_eq!(
        store
            .pending_agent_command(&did, agent_id)
            .await
            .unwrap()
            .map(|command| command.id),
        Some(pause.id)
    );
    let claimed_pause = store
        .claim_agent_commands("pause-integration-worker", 1)
        .await
        .unwrap();
    assert_eq!(claimed_pause.len(), 1);
    assert_eq!(claimed_pause[0].id, pause.id);
    let paused = store
        .complete_reconciled_agent_command(pause.id, &"f".repeat(64), None)
        .await
        .unwrap();
    assert_eq!(paused.status, "completed");
    assert_eq!(paused.agent_status, "paused");
    assert!(store
        .pending_agent_command(&did, agent_id)
        .await
        .unwrap()
        .is_none());
    sqlx::query("UPDATE agents SET status = 'provisioning' WHERE id = $1")
        .bind(agent_id)
        .execute(&pool)
        .await
        .unwrap();

    let future_snapshot_id = Uuid::new_v4();
    let future_observed_at = Utc::now() + Duration::hours(1);
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
        evidence_id = Uuid::new_v4();
        sqlx::query(
            r#"
            INSERT INTO agent_readiness_evidence (
                id, execution_account_id, snapshot_id, check_name, ready, source,
                evidence_digest, observed_at, expires_at
            ) VALUES ($1, $2, $3, $4, true, 'integration-test', $5, $6, $7)
            "#,
        )
        .bind(evidence_id)
        .bind(account_id)
        .bind(future_snapshot_id)
        .bind(check_name)
        .bind("2".repeat(64))
        .bind(future_observed_at)
        .bind(future_observed_at + Duration::seconds(30))
        .execute(&pool)
        .await
        .unwrap();
    }
    let future_readiness = sqlx::query_as::<_, (bool, bool)>(
        "SELECT lighter_linked, lighter_funded FROM current_agent_readiness WHERE execution_account_id = $1",
    )
    .bind(account_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(future_readiness, (false, false));
    assert!(
        sqlx::query("UPDATE agent_readiness_evidence SET ready = false WHERE id = $1")
            .bind(evidence_id)
            .execute(&pool)
            .await
            .is_err()
    );

    sqlx::query(
        r#"
        INSERT INTO execution_account_bindings (
            id, execution_account_id, venue, binding_ref, request_id, owner_address,
            lighter_account_index, lighter_api_key_index, status
        ) VALUES ($1, $2, 'lighter', $3, $4, $5, $6, 254, 'awaiting_signature')
        "#,
    )
    .bind(Uuid::new_v4())
    .bind(account_id)
    .bind(Uuid::new_v4())
    .bind(Uuid::new_v4())
    .bind(&registration.robinhood_owner)
    .bind(account_index)
    .execute(&pool)
    .await
    .unwrap();

    let other_user = Uuid::new_v4();
    let other_agent = Uuid::new_v4();
    let other_account = Uuid::new_v4();
    sqlx::query("INSERT INTO users (id, privy_did) VALUES ($1, $2)")
        .bind(other_user)
        .bind(format!("did:test:{other_user}"))
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query(
        "INSERT INTO agents (id, user_id, strategy_version, mode, status) VALUES ($1, $2, 'basis-aapl-v1', 'live', 'provisioning')",
    )
    .bind(other_agent)
    .bind(other_user)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO execution_accounts (id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status) VALUES ($1, $2, $3, 'basis-aapl-v1', 'c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32', 'provisioning')",
    )
    .bind(other_account)
    .bind(other_user)
    .bind(other_agent)
    .execute(&pool)
    .await
    .unwrap();
    assert!(sqlx::query(
        r#"
        INSERT INTO execution_account_bindings (
            id, execution_account_id, venue, binding_ref, request_id, owner_address,
            lighter_account_index, lighter_api_key_index, status
        ) VALUES ($1, $2, 'lighter', $3, $4, $5, $6, 254, 'awaiting_signature')
        "#,
    )
    .bind(Uuid::new_v4())
    .bind(other_account)
    .bind(Uuid::new_v4())
    .bind(Uuid::new_v4())
    .bind(random_address())
    .bind(account_index)
    .execute(&pool)
    .await
    .is_err());

    let command_id = Uuid::new_v4();
    sqlx::query(
        r#"
        INSERT INTO agent_commands (
            id, agent_id, execution_account_id, idempotency_key, command,
            status, agent_status, target_agent_status
        ) VALUES ($1, $2, $3, 'withdraw-integration', 'withdraw',
                  'pending', 'provisioning', 'provisioning')
        "#,
    )
    .bind(command_id)
    .bind(agent_id)
    .bind(account_id)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query("INSERT INTO agent_command_outbox (command_id) VALUES ($1)")
        .bind(command_id)
        .execute(&pool)
        .await
        .unwrap();
    let claimed = store
        .claim_agent_commands("integration-worker", 1)
        .await
        .unwrap();
    assert_eq!(claimed.len(), 1);
    assert_eq!(claimed[0].id, command_id);
    assert!(claimed[0].requested_at_ms > 0);
    assert_eq!(claimed[0].robinhood_owner, registration.robinhood_owner);
    assert_eq!(claimed[0].robinhood_vault, registration.robinhood_vault);
    let action = OwnerAction {
        chain_id: 4663,
        from: claimed[0].robinhood_owner.clone(),
        to: claimed[0].robinhood_vault.clone(),
        data: format!("0x142834dd{:064x}", 25_000_000),
        value: "0".into(),
    };
    store
        .await_agent_command_signature(command_id, &"a".repeat(64), &[action])
        .await
        .unwrap();
    let recovered = store.recover_agent_commands(1).await.unwrap();
    assert_eq!(recovered.len(), 1);
    assert_eq!(recovered[0].requested_at_ms, claimed[0].requested_at_ms);
    let record = store
        .complete_reconciled_agent_command(command_id, &"b".repeat(64), None)
        .await
        .unwrap();
    assert_eq!(record.status, "completed");
    assert!(record.owner_actions.is_empty());

    let blocked_command_id = Uuid::new_v4();
    sqlx::query(
        r#"
        INSERT INTO agent_commands (
            id, agent_id, execution_account_id, idempotency_key, command,
            status, agent_status, target_agent_status
        ) VALUES ($1, $2, $3, 'blocked-integration', 'pause',
                  'processing', 'provisioning', 'paused')
        "#,
    )
    .bind(blocked_command_id)
    .bind(agent_id)
    .bind(account_id)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query("INSERT INTO agent_command_outbox (command_id) VALUES ($1)")
        .bind(blocked_command_id)
        .execute(&pool)
        .await
        .unwrap();
    let blocked = store
        .complete_reconciled_agent_command(
            blocked_command_id,
            &"c".repeat(64),
            Some("coordinator blocked the command"),
        )
        .await
        .unwrap();
    assert_eq!(blocked.status, "failed");
    let lifecycle = sqlx::query_as::<_, (String, String, Option<String>)>(
        r#"
        SELECT account.status, agent.status, agent.blocked_reason
        FROM execution_accounts account
        JOIN agents agent ON agent.id = account.agent_id
        WHERE account.id = $1
        "#,
    )
    .bind(account_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        lifecycle,
        (
            "blocked".into(),
            "blocked".into(),
            Some("coordinator blocked the command".into())
        )
    );
}

#[tokio::test]
#[ignore = "requires APP_TEST_DATABASE_URL"]
async fn unprovisioned_agent_closes_locally_without_dispatch() {
    let database_url = std::env::var("APP_TEST_DATABASE_URL").expect("APP_TEST_DATABASE_URL");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database_url)
        .await
        .unwrap();
    sqlx::migrate!().run(&pool).await.unwrap();
    let (did, agent_id, account_id) = insert_provisioning_agent(&pool).await;

    let store = ProductStore::from_pool(pool.clone());
    for command in ["launch", "pause", "resume", "withdraw"] {
        let rejected = store
            .create_agent_command(
                &did,
                agent_id,
                &format!("pre-registration-{command}"),
                command,
            )
            .await
            .unwrap();
        assert_eq!(rejected.status, "rejected");
        assert_eq!(
            rejected.error_reason.as_deref(),
            Some("coordinator_account_not_registered")
        );
    }

    let closed = store
        .create_agent_command(&did, agent_id, "pre-registration-close", "close")
        .await
        .unwrap();
    assert_eq!(closed.status, "completed");
    assert_eq!(closed.agent_status, "closed");
    assert_eq!(closed.target_agent_status, "closed");
    assert!(closed.completed_at.is_some());
    assert!(closed.result_evidence_digest.is_none());
    assert!(closed.owner_actions.is_empty());

    let replay = store
        .create_agent_command(&did, agent_id, "pre-registration-close", "close")
        .await
        .unwrap();
    assert_eq!(replay.id, closed.id);
    let lifecycle = sqlx::query_as::<_, (String, String)>(
        r#"
        SELECT account.status, agent.status
        FROM execution_accounts account
        JOIN agents agent ON agent.id = account.agent_id
        WHERE account.id = $1
        "#,
    )
    .bind(account_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(lifecycle, ("closed".into(), "closed".into()));
    let outbox_rows = sqlx::query_scalar::<_, i64>(
        r#"
        SELECT count(*)
        FROM agent_command_outbox outbox
        JOIN agent_commands command ON command.id = outbox.command_id
        WHERE command.agent_id = $1
        "#,
    )
    .bind(agent_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(outbox_rows, 0);
    let next_agent = store
        .create_live_agent(&did, "basis-aapl-v1")
        .await
        .unwrap();
    assert_eq!(next_agent.id, agent_id);
    assert_eq!(next_agent.status, "setup");
    let next_account = store
        .create_execution_account(&did, next_agent.id)
        .await
        .unwrap();
    assert_eq!(next_account.id, account_id);
    assert_eq!(next_account.status, "provisioning");
}

#[tokio::test]
#[ignore = "requires APP_TEST_DATABASE_URL"]
async fn robinhood_authority_requires_registered_revocation_before_close() {
    let database_url = std::env::var("APP_TEST_DATABASE_URL").expect("APP_TEST_DATABASE_URL");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database_url)
        .await
        .unwrap();
    sqlx::migrate!().run(&pool).await.unwrap();
    let (did, agent_id, account_id) = insert_provisioning_agent(&pool).await;
    let owner = random_address().to_ascii_lowercase();
    let vault = random_address().to_ascii_lowercase();
    let signer = random_address().to_ascii_lowercase();
    let factory = random_address().to_ascii_lowercase();
    let registry = random_address().to_ascii_lowercase();
    let risk_manager = random_address().to_ascii_lowercase();
    let spot_adapter = random_address().to_ascii_lowercase();
    let policy_digest = random_hash();
    let deployment_hash = random_hash();
    let authorization_hash = random_hash();

    sqlx::query(
        r#"
        INSERT INTO execution_account_bindings (
            id, execution_account_id, venue, binding_ref, request_id,
            provider_request_id, owner_address, public_identifier,
            proof_transaction_hash, status, robinhood_vault_address,
            robinhood_signer_address, robinhood_key_version,
            robinhood_factory_address, robinhood_registry_address,
            robinhood_policy_digest, robinhood_risk_manager_address,
            robinhood_spot_adapter_address, robinhood_deployment_block,
            robinhood_authorization_transaction_hash,
            robinhood_authorization_block
        ) VALUES (
            $1, $2, 'robinhood', $3, $4, $2, $5, $6, $7, 'linked',
            $6, $8, 1, $9, $10, $11, $12, $13, 100, $14, 101
        )
        "#,
    )
    .bind(Uuid::new_v4())
    .bind(account_id)
    .bind(Uuid::new_v4())
    .bind(Uuid::new_v4())
    .bind(&owner)
    .bind(&vault)
    .bind(&deployment_hash)
    .bind(&signer)
    .bind(&factory)
    .bind(&registry)
    .bind(&policy_digest)
    .bind(&risk_manager)
    .bind(&spot_adapter)
    .bind(&authorization_hash)
    .execute(&pool)
    .await
    .unwrap();

    let store = ProductStore::from_pool(pool.clone());
    let rejected = store
        .create_agent_command(&did, agent_id, "robinhood-before-lighter-close", "close")
        .await
        .unwrap();
    assert_eq!(rejected.status, "rejected");
    assert_eq!(
        rejected.error_reason.as_deref(),
        Some("external_execution_authority_requires_reconciliation")
    );
    assert!(rejected.completed_at.is_none());
    assert!(rejected.owner_actions.is_empty());
    let lifecycle = sqlx::query_as::<_, (String, String)>(
        r#"
        SELECT account.status, agent.status
        FROM execution_accounts account
        JOIN agents agent ON agent.id = account.agent_id
        WHERE account.id = $1
        "#,
    )
    .bind(account_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(lifecycle, ("provisioning".into(), "provisioning".into()));
    let outbox_rows = sqlx::query_scalar::<_, i64>(
        r#"
        SELECT count(*)
        FROM agent_command_outbox outbox
        JOIN agent_commands command ON command.id = outbox.command_id
        WHERE command.agent_id = $1
        "#,
    )
    .bind(agent_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(outbox_rows, 0);

    let lighter_account_index = i64::from(u32::from_be_bytes(
        account_id.as_bytes()[..4].try_into().unwrap(),
    )) + 1;
    sqlx::query(
        r#"
        INSERT INTO execution_account_bindings (
            id, execution_account_id, venue, binding_ref, request_id,
            provider_request_id, owner_address, lighter_account_index,
            lighter_api_key_index, public_identifier, public_key,
            association_payload, proof_transaction_hash, status
        ) VALUES (
            $1, $2, 'lighter', $3, $4, $5, $6, $7, 254,
            $8, $9, 'owner-authorized', $10, 'linked'
        )
        "#,
    )
    .bind(Uuid::new_v4())
    .bind(account_id)
    .bind(Uuid::new_v4())
    .bind(Uuid::new_v4())
    .bind(Uuid::new_v4())
    .bind(&owner)
    .bind(lighter_account_index)
    .bind(format!("account:{lighter_account_index}:key:254"))
    .bind(random_hash())
    .bind(random_hash())
    .execute(&pool)
    .await
    .unwrap();

    let mut registration = AccountRegistration {
        execution_account_id: account_id,
        agent_id,
        strategy_version: "basis-aapl-v1".into(),
        risk_version: "basis-aapl-v1".into(),
        strategy_manifest_sha256:
            "c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32".into(),
        lighter_account_index,
        lighter_api_key_index: 254,
        robinhood_owner: owner.clone(),
        robinhood_vault: vault.clone(),
        robinhood_signer: signer,
        binding_sha256: String::new(),
    };
    registration.binding_sha256 = registration.calculate_binding_sha256();
    sqlx::query(
        r#"
        INSERT INTO coordinator_account_registrations (
            execution_account_id, agent_id, strategy_version, risk_version,
            strategy_manifest_sha256, lighter_account_index, lighter_api_key_index,
            robinhood_owner, robinhood_vault, robinhood_signer, binding_sha256,
            status, coordinator_account_status, coordinator_control_mode, registered_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
            'registered', 'active', 'ACTIVE', now()
        )
        "#,
    )
    .bind(registration.execution_account_id)
    .bind(registration.agent_id)
    .bind(&registration.strategy_version)
    .bind(&registration.risk_version)
    .bind(&registration.strategy_manifest_sha256)
    .bind(registration.lighter_account_index)
    .bind(registration.lighter_api_key_index)
    .bind(&registration.robinhood_owner)
    .bind(&registration.robinhood_vault)
    .bind(&registration.robinhood_signer)
    .bind(&registration.binding_sha256)
    .execute(&pool)
    .await
    .unwrap();

    let close = store
        .create_agent_command(&did, agent_id, "reconciled-close", "close")
        .await
        .unwrap();
    assert_eq!(close.status, "pending");
    assert_eq!(close.agent_status, "closing");
    let claimed = store
        .claim_agent_commands("reconciled-close-worker", 1)
        .await
        .unwrap();
    assert_eq!(claimed.len(), 1);
    assert_eq!(claimed[0].id, close.id);
    assert_eq!(claimed[0].lighter_owner.as_deref(), Some(owner.as_str()));
    assert_eq!(
        claimed[0].lighter_account_index,
        Some(lighter_account_index)
    );
    assert_eq!(claimed[0].lighter_api_key_index, Some(254));
    let halt = OwnerAction {
        chain_id: 4663,
        from: owner,
        to: vault,
        data: "0x51755334".into(),
        value: "0".into(),
    };
    store
        .await_agent_command_signature(close.id, &"a".repeat(64), &[halt])
        .await
        .unwrap();
    let awaiting_revocation = store.agent_command(&did, agent_id, close.id).await.unwrap();
    assert_eq!(awaiting_revocation.status, "awaiting_signature");
    assert_eq!(awaiting_revocation.owner_actions.len(), 1);
    assert!(awaiting_revocation.completed_at.is_none());
    let other_did = format!("did:test:{}", Uuid::new_v4());
    assert!(store
        .lighter_binding_identity(&other_did, agent_id)
        .await
        .is_err());
    assert!(store
        .agent_command(&other_did, agent_id, close.id)
        .await
        .is_err());
    assert!(store
        .pending_agent_command(&other_did, agent_id)
        .await
        .unwrap()
        .is_none());
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT status FROM agents WHERE id = $1")
            .bind(agent_id)
            .fetch_one(&pool)
            .await
            .unwrap(),
        "closing"
    );

    let closed = store
        .complete_reconciled_agent_command(close.id, &"b".repeat(64), None)
        .await
        .unwrap();
    assert_eq!(closed.status, "completed");
    assert_eq!(closed.agent_status, "closed");
    assert_eq!(
        store
            .create_live_agent(&did, "basis-aapl-v1")
            .await
            .unwrap_err()
            .to_string(),
        "governance_rotation_required"
    );
}

#[tokio::test]
#[ignore = "requires APP_TEST_DATABASE_URL"]
async fn release_rotation_terminalizes_non_close_commands_and_preserves_close() {
    let database_url = std::env::var("APP_TEST_DATABASE_URL").expect("APP_TEST_DATABASE_URL");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database_url)
        .await
        .unwrap();
    sqlx::migrate!().run(&pool).await.unwrap();
    let store = ProductStore::from_pool(pool.clone());

    for status in ["pending", "processing", "awaiting_signature"] {
        let (did, agent_id, account_id, registration) = insert_release_blocked_account(&pool).await;
        let stale_id = Uuid::new_v4();
        let stale_action = OwnerAction {
            chain_id: 4663,
            from: registration.robinhood_owner.clone(),
            to: registration.robinhood_vault.clone(),
            data: format!("0x142834dd{:064x}", 1),
            value: "0".into(),
        };
        sqlx::query(
            r#"
            INSERT INTO agent_commands (
                id, agent_id, execution_account_id, idempotency_key, command,
                status, agent_status, target_agent_status, dispatch_requested_at,
                result_evidence_digest, result_owner_actions
            ) VALUES (
                $1, $2, $3, $4, 'withdraw', $5, 'blocked', 'closed',
                CASE WHEN $5 = 'pending' THEN NULL ELSE now() END,
                $6, $7
            )
            "#,
        )
        .bind(stale_id)
        .bind(agent_id)
        .bind(account_id)
        .bind(format!("stale-{status}"))
        .bind(status)
        .bind("a".repeat(64))
        .bind(sqlx::types::Json(vec![stale_action.clone()]))
        .execute(&pool)
        .await
        .unwrap();
        sqlx::query("INSERT INTO agent_command_outbox (command_id) VALUES ($1)")
            .bind(stale_id)
            .execute(&pool)
            .await
            .unwrap();

        let claimed = store
            .claim_agent_commands(&format!("release-rotation-{status}"), 100)
            .await
            .unwrap();
        assert!(claimed.iter().all(|item| item.id != stale_id));
        let recovered = store.recover_agent_commands(100).await.unwrap();
        assert!(recovered.iter().all(|item| item.id != stale_id));

        let close = store
            .create_agent_command(&did, agent_id, &format!("release-close-{status}"), "close")
            .await
            .unwrap();
        assert_eq!(close.status, "pending");

        let terminal =
            sqlx::query_as::<_, (String, Option<String>, bool, i64, bool, Option<String>)>(
                r#"
            SELECT command.status, command.error_reason,
                command.completed_at IS NOT NULL,
                jsonb_array_length(command.result_owner_actions)::bigint,
                outbox.delivered_at IS NOT NULL, outbox.last_error
            FROM agent_commands command
            JOIN agent_command_outbox outbox ON outbox.command_id = command.id
            WHERE command.id = $1
            "#,
            )
            .bind(stale_id)
            .fetch_one(&pool)
            .await
            .unwrap();
        assert_eq!(
            terminal,
            (
                "failed".into(),
                Some("strategy_release_changed_close_required".into()),
                true,
                0,
                true,
                Some("strategy_release_changed_close_required".into())
            )
        );
        assert!(store
            .await_agent_command_signature(stale_id, &"b".repeat(64), &[stale_action])
            .await
            .is_err());
        assert!(store
            .complete_reconciled_agent_command(stale_id, &"c".repeat(64), None)
            .await
            .is_err());

        store
            .block_account_registration(
                account_id,
                "strategy release changed; registration must not be reused",
            )
            .await
            .unwrap();
        let preserved = sqlx::query_as::<_, (String, String, String)>(
            r#"
            SELECT command.status, command.agent_status, agent.status
            FROM agent_commands command
            JOIN agents agent ON agent.id = command.agent_id
            WHERE command.id = $1
            "#,
        )
        .bind(close.id)
        .fetch_one(&pool)
        .await
        .unwrap();
        assert_eq!(
            preserved,
            ("pending".into(), "blocked".into(), "blocked".into())
        );
        let claimed = store
            .claim_agent_commands(&format!("release-close-worker-{status}"), 100)
            .await
            .unwrap();
        assert!(claimed.iter().any(|item| item.id == close.id));
        let closed = store
            .complete_reconciled_agent_command(close.id, &"d".repeat(64), None)
            .await
            .unwrap();
        assert_eq!(closed.status, "completed");
        assert_eq!(closed.agent_status, "closed");
    }
}

#[tokio::test]
#[ignore = "requires APP_TEST_DATABASE_URL"]
async fn release_blocked_account_closes_but_requires_governance_rotation() {
    let database_url = std::env::var("APP_TEST_DATABASE_URL").expect("APP_TEST_DATABASE_URL");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database_url)
        .await
        .unwrap();
    sqlx::migrate!().run(&pool).await.unwrap();

    let user_id = Uuid::new_v4();
    let agent_id = Uuid::new_v4();
    let account_id = Uuid::new_v4();
    let did = format!("did:test:{user_id}");
    sqlx::query("INSERT INTO users (id, privy_did) VALUES ($1, $2)")
        .bind(user_id)
        .bind(&did)
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query(
        r#"
        INSERT INTO agents (
            id, user_id, strategy_version, mode, status, blocked_reason
        ) VALUES (
            $1, $2, 'basis-aapl-v1', 'live', 'blocked',
            'strategy release changed; reconcile before reprovisioning'
        )
        "#,
    )
    .bind(agent_id)
    .bind(user_id)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
        INSERT INTO execution_accounts (
            id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status
        ) VALUES (
            $1, $2, $3, 'basis-aapl-v1',
            'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f',
            'blocked'
        )
        "#,
    )
    .bind(account_id)
    .bind(user_id)
    .bind(agent_id)
    .execute(&pool)
    .await
    .unwrap();

    let snapshot_id = Uuid::new_v4();
    let observed_at = Utc::now();
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
            ) VALUES ($1, $2, $3, $4, false, 'release-upgrade-test', $5, $6, $7)
            "#,
        )
        .bind(Uuid::new_v4())
        .bind(account_id)
        .bind(snapshot_id)
        .bind(check_name)
        .bind("2".repeat(64))
        .bind(observed_at)
        .bind(observed_at + Duration::minutes(1))
        .execute(&pool)
        .await
        .unwrap();
    }

    let lighter_account_index = i64::from(u32::from_be_bytes(
        account_id.as_bytes()[..4].try_into().unwrap(),
    )) + 1;
    let mut registration = AccountRegistration {
        execution_account_id: account_id,
        agent_id,
        strategy_version: "basis-aapl-v1".into(),
        risk_version: "basis-aapl-v1".into(),
        strategy_manifest_sha256:
            "da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f".into(),
        lighter_account_index,
        lighter_api_key_index: 254,
        robinhood_owner: random_address().to_ascii_lowercase(),
        robinhood_vault: random_address().to_ascii_lowercase(),
        robinhood_signer: random_address().to_ascii_lowercase(),
        binding_sha256: String::new(),
    };
    registration.binding_sha256 = registration.calculate_binding_sha256();
    sqlx::query(
        r#"
        INSERT INTO coordinator_account_registrations (
            execution_account_id, agent_id, strategy_version, risk_version,
            strategy_manifest_sha256, lighter_account_index, lighter_api_key_index,
            robinhood_owner, robinhood_vault, robinhood_signer, binding_sha256,
            status, coordinator_account_status, coordinator_control_mode,
            registered_at, last_error
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
            'blocked', 'blocked', 'HALTED', now(),
            'strategy release changed; registration must not be reused'
        )
        "#,
    )
    .bind(registration.execution_account_id)
    .bind(registration.agent_id)
    .bind(&registration.strategy_version)
    .bind(&registration.risk_version)
    .bind(&registration.strategy_manifest_sha256)
    .bind(registration.lighter_account_index)
    .bind(registration.lighter_api_key_index)
    .bind(&registration.robinhood_owner)
    .bind(&registration.robinhood_vault)
    .bind(&registration.robinhood_signer)
    .bind(&registration.binding_sha256)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
        INSERT INTO execution_account_bindings (
            id, execution_account_id, venue, binding_ref, request_id, owner_address,
            lighter_account_index, lighter_api_key_index, public_identifier, status
        ) VALUES ($1, $2, 'lighter', $3, $4, $5, $6, 254, $7, 'linked')
        "#,
    )
    .bind(Uuid::new_v4())
    .bind(account_id)
    .bind(Uuid::new_v4())
    .bind(Uuid::new_v4())
    .bind(&registration.robinhood_owner)
    .bind(lighter_account_index)
    .bind(format!("account:{lighter_account_index}:key:254"))
    .execute(&pool)
    .await
    .unwrap();

    let store = ProductStore::from_pool(pool.clone());
    let close = store
        .create_agent_command(&did, agent_id, "release-upgrade-close", "close")
        .await
        .unwrap();
    assert_eq!(close.status, "pending");
    assert_eq!(close.agent_status, "closing");
    let claimed = store
        .claim_agent_commands("release-upgrade-worker", 1)
        .await
        .unwrap();
    assert_eq!(claimed.len(), 1);
    assert_eq!(claimed[0].id, close.id);
    let closed = store
        .complete_reconciled_agent_command(close.id, &"3".repeat(64), None)
        .await
        .unwrap();
    assert_eq!(closed.status, "completed");
    assert_eq!(closed.agent_status, "closed");

    let error = store
        .create_live_agent(&did, "basis-aapl-v1")
        .await
        .unwrap_err();
    assert_eq!(error.to_string(), "governance_rotation_required");
}

#[tokio::test]
#[ignore = "requires APP_TEST_DATABASE_URL"]
async fn robinhood_graph_binding_is_immutable_and_provisioner_authoritative() {
    let database_url = std::env::var("APP_TEST_DATABASE_URL").expect("APP_TEST_DATABASE_URL");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database_url)
        .await
        .unwrap();
    sqlx::migrate!().run(&pool).await.unwrap();
    let did = format!("did:test:{}", Uuid::new_v4());
    let user_id = Uuid::new_v4();
    let agent_id = Uuid::new_v4();
    let account_id = Uuid::new_v4();
    let owner = random_address();
    let signer = random_address();
    let factory = random_address();
    let registry = random_address();
    let risk_manager = random_address();
    let spot_adapter = random_address();
    let vault = random_address();
    sqlx::query("INSERT INTO users (id, privy_did) VALUES ($1, $2)")
        .bind(user_id)
        .bind(&did)
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query(
        r#"
        INSERT INTO wallet_links (
            id, user_id, chain_namespace, address, wallet_type, is_primary, verified_at
        ) VALUES ($1, $2, 'eip155', $3, 'injected', true, now())
        "#,
    )
    .bind(Uuid::new_v4())
    .bind(user_id)
    .bind(&owner)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO agents (id, user_id, strategy_version, mode, status) VALUES ($1, $2, 'basis-aapl-v1', 'live', 'provisioning')",
    )
    .bind(agent_id)
    .bind(user_id)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO execution_accounts (id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status) VALUES ($1, $2, $3, 'basis-aapl-v1', 'c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32', 'provisioning')",
    )
    .bind(account_id)
    .bind(user_id)
    .bind(agent_id)
    .execute(&pool)
    .await
    .unwrap();

    let store = ProductStore::from_pool(pool.clone());
    let pending = store
        .request_execution_binding(&did, agent_id, "robinhood", &owner)
        .await
        .unwrap();
    let prepared = PublicGraphBinding {
        execution_account_id: account_id,
        owner_address: owner.clone(),
        signer_address: signer.clone(),
        key_version: 1,
        factory_address: factory.clone(),
        registry_address: registry,
        policy_digest: format!("0x{}", "55".repeat(32)),
        graph: Graph {
            risk_manager,
            spot_adapter,
            vault,
        },
        status: "awaiting_deployment".into(),
        deployment_transaction_hash: None,
        deployment_block: None,
        authorization_transaction_hash: None,
        authorization_block: None,
        actions: vec![UnsignedAction {
            kind: "deploy_user_graph".into(),
            chain_id: "4663".into(),
            to: factory,
            value: "0".into(),
            data: deploy_data(&owner),
        }],
        updated_at: Utc::now().to_rfc3339(),
    };
    let bound = store
        .apply_robinhood_prepare(&did, agent_id, pending.request_id, &prepared)
        .await
        .unwrap();
    assert_eq!(bound.provider_request_id, Some(account_id));
    assert!(bound
        .robinhood_signer_address
        .as_deref()
        .is_some_and(|value| value.eq_ignore_ascii_case(&signer)));
    assert!(bound.robinhood_deployment_action.is_some());

    let mut substituted = prepared.clone();
    substituted.signer_address = random_address();
    assert!(store
        .apply_robinhood_prepare(&did, agent_id, pending.request_id, &substituted)
        .await
        .is_err());

    let deployment_transaction_hash = random_hash();
    let mut confirmed = prepared;
    confirmed.status = "confirming".into();
    confirmed.actions = vec![UnsignedAction {
        kind: "authorize_execution_agent".into(),
        chain_id: "4663".into(),
        to: confirmed.graph.vault.clone(),
        value: "0".into(),
        data: authorize_data(&confirmed.signer_address),
    }];
    confirmed.deployment_transaction_hash = Some(deployment_transaction_hash.clone());
    confirmed.deployment_block = Some(123);
    let bound = store
        .apply_robinhood_confirmation(
            &did,
            agent_id,
            pending.request_id,
            &deployment_transaction_hash,
            &confirmed,
        )
        .await
        .unwrap();
    assert_eq!(bound.status, "awaiting_signature");
    assert_eq!(
        bound.proof_transaction_hash,
        Some(deployment_transaction_hash)
    );
    assert!(bound.robinhood_deployment_action.is_some());

    let authorization_transaction_hash = random_hash();
    confirmed.status = "active".into();
    confirmed.actions.clear();
    confirmed.authorization_transaction_hash = Some(authorization_transaction_hash.clone());
    confirmed.authorization_block = Some(124);
    let bound = store
        .apply_robinhood_confirmation(
            &did,
            agent_id,
            pending.request_id,
            &authorization_transaction_hash,
            &confirmed,
        )
        .await
        .unwrap();
    assert_eq!(bound.status, "linked");
    assert_eq!(bound.robinhood_deployment_block, Some(123));
    assert_eq!(
        bound.robinhood_authorization_transaction_hash,
        Some(authorization_transaction_hash)
    );
    assert_eq!(bound.robinhood_authorization_block, Some(124));
    assert!(bound.robinhood_deployment_action.is_none());

    let readiness = store.agent_readiness(&did, agent_id).await.unwrap();
    assert!(readiness
        .robinhood_owner_address
        .as_deref()
        .is_some_and(|value| value.eq_ignore_ascii_case(&confirmed.owner_address)));
    assert!(readiness
        .robinhood_vault_address
        .as_deref()
        .is_some_and(|value| value.eq_ignore_ascii_case(&confirmed.graph.vault)));

    let account_index = i64::from(u32::from_be_bytes(
        account_id.as_bytes()[..4].try_into().unwrap(),
    )) + 1;
    sqlx::query(
        r#"
        INSERT INTO execution_account_bindings (
            id, execution_account_id, venue, binding_ref, request_id, owner_address,
            lighter_account_index, lighter_api_key_index, public_identifier,
            public_key, status
        ) VALUES ($1, $2, 'lighter', $3, $4, $5, $6, 254, $7, $8, 'linked')
        "#,
    )
    .bind(Uuid::new_v4())
    .bind(account_id)
    .bind(Uuid::new_v4())
    .bind(Uuid::new_v4())
    .bind(random_address())
    .bind(account_index)
    .bind(format!("account:{account_index}:key:254"))
    .bind(Uuid::new_v4().to_string())
    .execute(&pool)
    .await
    .unwrap();
    store.enqueue_ready_account_registrations(10).await.unwrap();
    let claimed = store
        .claim_account_registrations("registration-integration-worker", 10)
        .await
        .unwrap();
    assert_eq!(claimed.len(), 1);
    let registration = claimed.into_iter().next().unwrap();
    assert_eq!(registration.execution_account_id, account_id);
    assert_eq!(
        registration.binding_sha256,
        registration.calculate_binding_sha256()
    );
    store
        .retry_account_registration(account_id, "temporary coordinator outage")
        .await
        .unwrap();
    let retry_state = sqlx::query_as::<_, (String, bool)>(
        r#"
        SELECT registration.status, outbox.claimed_at IS NULL
        FROM coordinator_account_registrations registration
        JOIN coordinator_account_registration_outbox outbox USING (execution_account_id)
        WHERE registration.execution_account_id = $1
        "#,
    )
    .bind(account_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(retry_state, ("pending".into(), true));
    sqlx::query(
        "UPDATE coordinator_account_registration_outbox SET available_at = now() WHERE execution_account_id = $1",
    )
    .bind(account_id)
    .execute(&pool)
    .await
    .unwrap();
    let reclaimed = store
        .claim_account_registrations("registration-integration-worker", 10)
        .await
        .unwrap();
    assert_eq!(reclaimed.as_slice(), std::slice::from_ref(&registration));
    let response = AccountRegistrationResponse {
        execution_account_id: account_id.to_string(),
        agent_id: agent_id.to_string(),
        strategy_version: registration.strategy_version.clone(),
        risk_version: registration.risk_version.clone(),
        strategy_manifest_sha256: registration.strategy_manifest_sha256.clone(),
        lighter_account_index: registration.lighter_account_index,
        lighter_api_key_index: registration.lighter_api_key_index,
        robinhood_owner: registration.robinhood_owner.clone(),
        robinhood_vault: registration.robinhood_vault.clone(),
        robinhood_signer: registration.robinhood_signer.clone(),
        binding_sha256: registration.binding_sha256.clone(),
        account_status: "active".into(),
        control_mode: "HALTED".into(),
        readiness: AccountRegistrationReadiness {
            venue_approved: false,
            oracle_healthy: false,
            sequencer_healthy: false,
            reconciliation_ready: false,
            exit_authority_ready: false,
            alerting_ready: false,
            safe_rotation_ready: false,
        },
    };
    store
        .complete_account_registration(&registration, &response)
        .await
        .unwrap();
    let status = sqlx::query_as::<_, (String, Option<String>, Option<String>, bool)>(
        r#"
        SELECT registration.status, registration.coordinator_account_status,
            registration.coordinator_control_mode, outbox.delivered_at IS NOT NULL
        FROM coordinator_account_registrations registration
        JOIN coordinator_account_registration_outbox outbox USING (execution_account_id)
        WHERE registration.execution_account_id = $1
        "#,
    )
    .bind(account_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        status,
        (
            "registered".into(),
            Some("active".into()),
            Some("HALTED".into()),
            true
        )
    );
    store.enqueue_ready_account_registrations(10).await.unwrap();
    assert!(store
        .claim_account_registrations("registration-integration-worker", 10)
        .await
        .unwrap()
        .is_empty());

    let mut inactive = response.clone();
    inactive.account_status = "closed".into();
    assert!(store
        .complete_account_registration(&registration, &inactive)
        .await
        .is_err());
    let mut substituted = response;
    substituted.robinhood_signer = random_address().to_ascii_lowercase();
    assert!(store
        .complete_account_registration(&registration, &substituted)
        .await
        .is_err());
    let blocked = sqlx::query_as::<_, (String, String)>(
        r#"
        SELECT account.status, agent.status
        FROM execution_accounts account JOIN agents agent ON agent.id = account.agent_id
        WHERE account.id = $1
        "#,
    )
    .bind(account_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(blocked, ("blocked".into(), "blocked".into()));
    assert!(store
        .request_execution_binding(&did, agent_id, "robinhood", &confirmed.owner_address)
        .await
        .is_err());
    sqlx::query("UPDATE execution_accounts SET status = 'awaiting_funding' WHERE id = $1")
        .bind(account_id)
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query("UPDATE agents SET status = 'closing' WHERE id = $1")
        .bind(agent_id)
        .execute(&pool)
        .await
        .unwrap();
    assert!(store
        .request_execution_binding(&did, agent_id, "robinhood", &confirmed.owner_address)
        .await
        .is_err());
    sqlx::query("UPDATE execution_accounts SET status = 'closed' WHERE id = $1")
        .bind(account_id)
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query("UPDATE agents SET status = 'closed' WHERE id = $1")
        .bind(agent_id)
        .execute(&pool)
        .await
        .unwrap();
    assert!(store
        .request_execution_binding(&did, agent_id, "robinhood", &confirmed.owner_address)
        .await
        .is_err());
}
use app::account_registration::{AccountRegistrationReadiness, AccountRegistrationResponse};
