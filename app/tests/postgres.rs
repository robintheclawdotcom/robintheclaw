use chrono::{Duration, Utc};
use sqlx::postgres::PgPoolOptions;
use uuid::Uuid;

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
}
