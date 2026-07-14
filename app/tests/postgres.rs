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

#[tokio::test]
#[ignore = "requires APP_TEST_DATABASE_URL"]
async fn readiness_is_complete_fresh_append_only_and_tenant_unique() {
    let database_url = std::env::var("APP_TEST_DATABASE_URL").expect("APP_TEST_DATABASE_URL");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database_url)
        .await
        .unwrap();
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
        "INSERT INTO execution_accounts (id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status) VALUES ($1, $2, $3, 'basis-aapl-v1', 'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f', 'provisioning')",
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
            "da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f".into(),
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
        "INSERT INTO execution_accounts (id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status) VALUES ($1, $2, $3, 'basis-aapl-v1', 'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f', 'provisioning')",
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
async fn unregistered_agent_closes_locally_without_dispatch() {
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
        "INSERT INTO agents (id, user_id, strategy_version, mode, status) VALUES ($1, $2, 'basis-aapl-v1', 'live', 'provisioning')",
    )
    .bind(agent_id)
    .bind(user_id)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO execution_accounts (id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status) VALUES ($1, $2, $3, 'basis-aapl-v1', 'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f', 'provisioning')",
    )
    .bind(account_id)
    .bind(user_id)
    .bind(agent_id)
    .execute(&pool)
    .await
    .unwrap();

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

    let mut registration = AccountRegistration {
        execution_account_id: account_id,
        agent_id,
        strategy_version: "basis-aapl-v1".into(),
        risk_version: "basis-aapl-v1".into(),
        strategy_manifest_sha256:
            "da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f".into(),
        lighter_account_index: i64::from(u32::from_be_bytes(
            account_id.as_bytes()[..4].try_into().unwrap(),
        )) + 1,
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
            robinhood_owner, robinhood_vault, robinhood_signer, binding_sha256
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
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
        "INSERT INTO coordinator_account_registration_outbox (execution_account_id) VALUES ($1)",
    )
    .bind(account_id)
    .execute(&pool)
    .await
    .unwrap();

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
    let registration_state = sqlx::query_as::<_, (String, Option<String>, bool)>(
        r#"
        SELECT registration.status, registration.last_error,
            outbox.delivered_at IS NOT NULL
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
        registration_state,
        (
            "blocked".into(),
            Some("owner_closed_before_registration".into()),
            true
        )
    );
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
        "INSERT INTO execution_accounts (id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status) VALUES ($1, $2, $3, 'basis-aapl-v1', 'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f', 'provisioning')",
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
