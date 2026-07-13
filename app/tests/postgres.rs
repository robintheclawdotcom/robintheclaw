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
    sqlx::query("INSERT INTO users (id, privy_did) VALUES ($1, $2)")
        .bind(user_id)
        .bind(format!("did:test:{user_id}"))
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
        "INSERT INTO execution_accounts (id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status) VALUES ($1, $2, $3, 'basis-aapl-v1', '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a', 'provisioning')",
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
        ) VALUES ($1, $2, 'lighter', $3, $4, $5, 71, 254, 'awaiting_signature')
        "#,
    )
    .bind(Uuid::new_v4())
    .bind(account_id)
    .bind(Uuid::new_v4())
    .bind(Uuid::new_v4())
    .bind("0x1111111111111111111111111111111111111111")
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
        "INSERT INTO execution_accounts (id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status) VALUES ($1, $2, $3, 'basis-aapl-v1', '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a', 'provisioning')",
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
        ) VALUES ($1, $2, 'lighter', $3, $4, $5, 71, 254, 'awaiting_signature')
        "#,
    )
    .bind(Uuid::new_v4())
    .bind(other_account)
    .bind(Uuid::new_v4())
    .bind(Uuid::new_v4())
    .bind("0x2222222222222222222222222222222222222222")
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
    let store = ProductStore::from_pool(pool.clone());
    let claimed = store
        .claim_agent_commands("integration-worker", 1)
        .await
        .unwrap();
    assert_eq!(claimed.len(), 1);
    assert_eq!(claimed[0].id, command_id);
    assert!(claimed[0].requested_at_ms > 0);
    let action = OwnerAction {
        chain_id: 4663,
        from: "0x1111111111111111111111111111111111111111".into(),
        to: "0x2222222222222222222222222222222222222222".into(),
        data: "0x12345678".into(),
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
        "INSERT INTO execution_accounts (id, user_id, agent_id, strategy_version, strategy_manifest_sha256, status) VALUES ($1, $2, $3, 'basis-aapl-v1', '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a', 'provisioning')",
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

    let transaction_hash = random_hash();
    let mut confirmed = prepared;
    confirmed.status = "active".into();
    confirmed.actions.clear();
    confirmed.deployment_transaction_hash = Some(transaction_hash.clone());
    confirmed.deployment_block = Some(123);
    let bound = store
        .apply_robinhood_confirmation(
            &did,
            agent_id,
            pending.request_id,
            &transaction_hash,
            &confirmed,
        )
        .await
        .unwrap();
    assert_eq!(bound.status, "linked");
    assert_eq!(bound.proof_transaction_hash, Some(transaction_hash));
    assert_eq!(bound.robinhood_deployment_block, Some(123));
    assert!(bound.robinhood_deployment_action.is_none());
}
