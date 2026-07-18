use coordinator::store::{
    calculate_intent_payload_sha256, AccountCommandRequest, AccountCommandStatusRequest,
    AccountRegistrationRequest, ActionKind, ExitRequest, ExitStatusRequest, IntentStatusRequest,
    NewAccountSnapshot, NewMarketQuote, NewVenueEvent, NextAction, ObservationOutcome,
    RecoveryRequest, Store, StoreError,
};
use execution::{
    ExecutionEvent, ExecutionSaga, ExecutionState, FrozenEvidence, PairIntent, PerpSide, SpotSide,
    BASIS_AAPL_V1_LEGACY_MANIFEST_SHA256, BASIS_AAPL_V1_MANIFEST_SHA256, CANARY_RISK_VERSION,
    PAIR_INTENT_VERSION,
};
use research::PromotionEvidence;
use sqlx::PgPool;
use std::time::Duration;

const BASIS_AAPL_V1_ROUTE_SHA256: &str =
    "77d59f5e80e76ed507522b27ee6b7ddd1f8395f0337f0b230c5bba64bb335590";
const PRIOR_BASIS_AAPL_V1_MANIFEST_SHA256: &str =
    "da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f";

#[tokio::test]
#[ignore = "requires a disposable PostgreSQL database"]
async fn migration_and_promotion_gate_are_enforced() {
    let url = std::env::var("TEST_DATABASE_URL").expect("TEST_DATABASE_URL is required");
    let pool = PgPool::connect(&url).await.unwrap();
    for migration in [
        include_str!("../migrations/0001_execution.sql"),
        include_str!("../migrations/0002_execution_actions.sql"),
        include_str!("../migrations/0003_venue_event_binding.sql"),
        include_str!("../migrations/0004_market_authority.sql"),
        include_str!("../migrations/0005_exit_authority.sql"),
    ] {
        sqlx::raw_sql(migration).execute(&pool).await.unwrap();
    }
    let legacy_id = "0x9999999999999999999999999999999999999999999999999999999999999999";
    sqlx::query(
        r#"
        INSERT INTO execution_intents
            (id, strategy_version, symbol, direction, payload, saga)
        VALUES ($1, 'legacy-v1', 'AAPL', 'long_spot_short_perp', '{}'::jsonb, '{}'::jsonb)
        "#,
    )
    .bind(legacy_id)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!(
        "../migrations/0006_multi_account_execution.sql"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!("../migrations/0007_account_commands.sql"))
        .execute(&pool)
        .await
        .unwrap();
    sqlx::raw_sql(include_str!("../migrations/0008_account_registration.sql"))
        .execute(&pool)
        .await
        .unwrap();
    sqlx::raw_sql(include_str!("../migrations/0009_intent_idempotency.sql"))
        .execute(&pool)
        .await
        .unwrap();
    sqlx::raw_sql(include_str!("../migrations/0010_exit_dispatch.sql"))
        .execute(&pool)
        .await
        .unwrap();
    sqlx::raw_sql(include_str!("../migrations/0011_operator_restrictions.sql"))
        .execute(&pool)
        .await
        .unwrap();
    sqlx::raw_sql(include_str!(
        "../migrations/0012_internal_canary_promotion.sql"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!(
        "../migrations/0013_derived_canary_readiness.sql"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!(
        "../migrations/0014_open_episode_resolution.sql"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!("../migrations/0015_exit_execution_policy.sql"))
        .execute(&pool)
        .await
        .unwrap();
    sqlx::raw_sql(include_str!(
        "../migrations/0019_robinhood_snapshot_source_blocks.sql"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!(
        "../migrations/0020_release_blocked_exit_quotes.sql"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!(
        "../migrations/0021_require_runtime_readiness.sql"
    ))
    .execute(&pool)
    .await
    .unwrap();
    for migration in [
        include_str!("../../runtime/live-scheduler/migrations/0001_live_scheduler.sql"),
        include_str!("../../runtime/live-scheduler/migrations/0002_natural_strategy_exit.sql"),
    ] {
        sqlx::raw_sql(migration).execute(&pool).await.unwrap();
    }
    let legacy = sqlx::query_as::<_, (String, bool, bool)>(
        "SELECT execution_account_id, active, payload_digest_required FROM execution_intents WHERE id = $1",
    )
    .bind(legacy_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(legacy, ("singleton-mainnet-canary".into(), false, false));
    let rollout = sqlx::query_as::<_, (bool, bool)>(
        "SELECT alerting_ready, safe_rotation_ready FROM execution_rollout_readiness WHERE singleton",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(rollout, (false, false));
    let activation_trigger = sqlx::query_scalar::<_, bool>(
        "SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'execution_promoted_canary_activation' AND NOT tgisinternal)",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(!activation_trigger);

    let evidence = approved_evidence();
    let digest = evidence.calculate_hash();
    sqlx::query(
        "INSERT INTO execution_promotion_evidence \
         (strategy_version, evidence, evidence_sha256, approved_by) VALUES ($1, $2, $3, $4)",
    )
    .bind(CANARY_RISK_VERSION)
    .bind(sqlx::types::Json(&evidence))
    .bind(&digest)
    .bind("approval-record")
    .execute(&pool)
    .await
    .unwrap();

    let store = Store::from_pool(pool.clone());
    assert!(matches!(
        store.create_intent(&intent(), 1_200).await,
        Err(StoreError::CoordinatorHalted)
    ));
    sqlx::query(
        "UPDATE execution_control SET mode = 'ACTIVE', reason = 'integration test' WHERE singleton",
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
        UPDATE execution_accounts
        SET strategy_version = $1, risk_version = $1, status = 'active',
            lighter_account_index = 7, lighter_api_key_index = 4,
            robinhood_vault = '0x0000000000000000000000000000000000000002',
            robinhood_signer = '0x0000000000000000000000000000000000000003',
            owner_address = '0x0000000000000000000000000000000000000004',
            strategy_manifest_sha256 = $2,
            binding_sha256 = repeat('a', 64)
        WHERE execution_account_id = 'singleton-mainnet-canary'
        "#,
    )
    .bind(CANARY_RISK_VERSION)
    .bind(execution::BASIS_AAPL_V1_MANIFEST_SHA256)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
        INSERT INTO execution_account_registrations
            (execution_account_id, agent_id, strategy_version, risk_version,
             strategy_manifest_sha256, lighter_account_index, lighter_api_key_index,
             robinhood_owner, robinhood_vault, robinhood_signer, binding_sha256)
        VALUES ('singleton-mainnet-canary', 'singleton-mainnet-canary', $1, $1, $2, 7, 4,
                '0x0000000000000000000000000000000000000004',
                '0x0000000000000000000000000000000000000002',
                '0x0000000000000000000000000000000000000003', repeat('a', 64))
        "#,
    )
    .bind(CANARY_RISK_VERSION)
    .bind(execution::BASIS_AAPL_V1_MANIFEST_SHA256)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
        INSERT INTO execution_strategy_control
            (strategy_version, strategy_manifest_sha256, mode, reason)
        VALUES ($1, $2, 'ACTIVE', 'integration test')
        ON CONFLICT (strategy_version) DO UPDATE SET
            strategy_manifest_sha256 = EXCLUDED.strategy_manifest_sha256,
            mode = EXCLUDED.mode,
            reason = EXCLUDED.reason
        "#,
    )
    .bind(CANARY_RISK_VERSION)
    .bind(execution::BASIS_AAPL_V1_MANIFEST_SHA256)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE execution_account_control SET mode = 'REDUCE_ONLY', reason = 'integration test awaiting launch'",
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
        UPDATE execution_account_readiness
        SET venue_approved = TRUE, oracle_healthy = TRUE, sequencer_healthy = TRUE,
            reconciliation_ready = TRUE, exit_authority_ready = TRUE,
            alerting_ready = TRUE, safe_rotation_ready = TRUE
        "#,
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
        UPDATE execution_rollout_readiness
        SET alerting_ready = TRUE, safe_rotation_ready = TRUE,
            version = version + 1, updated_at = now()
        WHERE singleton
        "#,
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
        INSERT INTO execution_market_configs
            (manifest_id, symbol, spot_token, lighter_market_index, spot_decimals,
             perp_base_decimals, perp_price_decimals, spot_config_version, ui_multiplier_e18,
             max_price_deviation_bps, max_spot_slippage_bps,
             max_unwind_price_deviation_bps, review_record_sha256, valid_from, valid_until)
        VALUES ($1, 'AAPL', $2, 101, 6, 6, 3, 1, $3, 100, 500, 2500, $4,
                TIMESTAMPTZ 'epoch', TIMESTAMPTZ 'epoch' + interval '1 day')
        "#,
    )
    .bind(&intent().evidence.market_manifest)
    .bind(&intent().spot_token)
    .bind(intent().evidence.ui_multiplier_e18.to_string())
    .bind("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
    .execute(&pool)
    .await
    .unwrap();
    let market_quote = NewMarketQuote {
        source: "lighter-auth".into(),
        source_session: "quote-session-1".into(),
        source_event_id: "quote-1".into(),
        source_sequence: 1,
        execution_account_id: None,
        market_manifest: intent().evidence.market_manifest,
        strategy_manifest_sha256: None,
        target_strategy_manifest_sha256: None,
        route_sha256: None,
        lighter_market_index: None,
        quote_block_hash: intent().evidence.quote_block_hash,
        mark_price: 25_000,
        expected_ui_multiplier: intent().expected_ui_multiplier.to_string(),
        min_oracle_round_id: intent().min_oracle_round_id.to_string(),
        publisher_at_ms: 899,
        received_at_ms: 900,
        expires_at_ms: 1_500,
        intent_id: None,
        spot_unwind_amount_in: None,
        spot_unwind_expected_amount_out: None,
        unwind_phase: None,
        perp_unwind_base_amount: None,
        perp_unwind_limit_price: None,
        submission_deadline_ms: None,
        reconciliation_deadline_ms: None,
    };
    assert!(
        store
            .record_market_quote(&market_quote)
            .await
            .unwrap()
            .created
    );
    assert!(
        !store
            .record_market_quote(&market_quote)
            .await
            .unwrap()
            .created
    );
    let mut duplicate_quote = market_quote.clone();
    duplicate_quote.source_event_id = "quote-duplicate".into();
    duplicate_quote.source_sequence = 2;
    assert!(
        store
            .record_market_quote(&duplicate_quote)
            .await
            .unwrap()
            .created
    );
    let mut snapshots = account_snapshots();
    for snapshot in &snapshots {
        assert!(store.record_account_snapshot(snapshot).await.unwrap());
    }
    let robinhood = snapshots
        .iter_mut()
        .find(|snapshot| snapshot.source == "robinhood-chain")
        .unwrap();
    robinhood.source_sequence = 2;
    robinhood.payload["global_mode"] = serde_json::json!("REDUCE_ONLY");
    assert!(store.record_account_snapshot(robinhood).await.unwrap());
    assert!(matches!(
        store
            .submit_account_command(
                &AccountCommandRequest {
                    command_id: "command-launch-global-race".into(),
                    execution_account_id: "singleton-mainnet-canary".into(),
                    agent_id: "singleton-mainnet-canary".into(),
                    command: "launch".into(),
                    requested_at_ms: 1_200,
                },
                1_200,
            )
            .await,
        Err(StoreError::AccountCommandBlocked)
    ));

    robinhood.source_sequence = 3;
    robinhood.payload["global_mode"] = serde_json::json!("ACTIVE");
    robinhood.payload["finalized_agent_address"] =
        serde_json::json!("0x0000000000000000000000000000000000000005");
    assert!(store.record_account_snapshot(robinhood).await.unwrap());
    assert!(matches!(
        store
            .submit_account_command(
                &AccountCommandRequest {
                    command_id: "command-launch-finalized-signer-race".into(),
                    execution_account_id: "singleton-mainnet-canary".into(),
                    agent_id: "singleton-mainnet-canary".into(),
                    command: "launch".into(),
                    requested_at_ms: 1_200,
                },
                1_200,
            )
            .await,
        Err(StoreError::AccountCommandBlocked)
    ));

    robinhood.source_sequence = 4;
    robinhood.payload["finalized_agent_address"] =
        serde_json::json!("0x0000000000000000000000000000000000000003");
    assert!(store.record_account_snapshot(robinhood).await.unwrap());
    let launch = store
        .submit_account_command(
            &AccountCommandRequest {
                command_id: "command-launch-canary-1".into(),
                execution_account_id: "singleton-mainnet-canary".into(),
                agent_id: "singleton-mainnet-canary".into(),
                command: "launch".into(),
                requested_at_ms: 1_200,
            },
            1_200,
        )
        .await
        .unwrap();
    assert_eq!(launch.status, "completed");
    assert!(launch.reconciled_flat);
    let mut mismatched_market = intent();
    mismatched_market.symbol = "AMD".into();
    mismatched_market.derive_identifiers().unwrap();
    assert!(matches!(
        store.create_intent(&mismatched_market, 1_200).await,
        Err(StoreError::InvalidIntent(_))
    ));
    let prepromotion = store.create_intent(&intent(), 1_200).await;
    assert!(
        matches!(&prepromotion, Err(StoreError::MissingEvidence)),
        "pre-promotion admission returned {prepromotion:?}"
    );

    let skipped =
        insert_transition(&pool, CANARY_RISK_VERSION, "registered", "shadow", &digest).await;
    assert!(skipped.is_err());

    for (from, to) in [
        ("registered", "research"),
        ("research", "shadow_eligible"),
        ("shadow_eligible", "shadow"),
        ("shadow", "audit_ready"),
        ("audit_ready", "canary_eligible"),
    ] {
        insert_transition(&pool, CANARY_RISK_VERSION, from, to, &digest)
            .await
            .unwrap();
    }

    let admitted = store.create_intent(&intent(), 1_200).await.unwrap();
    assert!(admitted.created);
    assert_eq!(admitted.saga.state, ExecutionState::Prechecked);
    let digest_required = sqlx::query_scalar::<_, bool>(
        "SELECT payload_digest_required FROM execution_intents WHERE id = $1",
    )
    .bind(intent().id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(digest_required);
    let mut admission_lock = pool.begin().await.unwrap();
    sqlx::query("SELECT pg_advisory_xact_lock(hashtext($1))")
        .bind(intent().id)
        .execute(&mut *admission_lock)
        .await
        .unwrap();
    let status_store = store.clone();
    let status_request = IntentStatusRequest {
        intent_id: intent().id,
        payload_sha256: admitted.payload_sha256.clone(),
    };
    let mut locked_status =
        tokio::spawn(async move { status_store.intent_status(&status_request).await });
    assert!(
        tokio::time::timeout(std::time::Duration::from_millis(100), &mut locked_status)
            .await
            .is_err()
    );
    admission_lock.commit().await.unwrap();
    assert_eq!(locked_status.await.unwrap().unwrap().status, "persisted");
    let duplicate = store.create_intent(&intent(), 9_999_999).await.unwrap();
    assert!(!duplicate.created);
    assert_eq!(duplicate.saga, admitted.saga);
    assert_eq!(duplicate.payload_sha256, admitted.payload_sha256);
    let persisted = store
        .intent_status(&IntentStatusRequest {
            intent_id: intent().id,
            payload_sha256: admitted.payload_sha256.clone(),
        })
        .await
        .unwrap();
    assert_eq!(persisted.status, "persisted");
    assert_eq!(persisted.saga, Some(admitted.saga.clone()));
    let execution = store
        .account_execution_status("singleton-mainnet-canary")
        .await
        .unwrap();
    assert!(execution.active);
    assert!(!execution.flat);
    assert_eq!(execution.intent_id, Some(intent().id));
    assert_eq!(execution.state, "prechecked");
    sqlx::query("UPDATE execution_control SET mode = 'HALTED' WHERE singleton")
        .execute(&pool)
        .await
        .unwrap();
    assert_eq!(
        store
            .account_execution_status("singleton-mainnet-canary")
            .await
            .unwrap()
            .control_mode,
        "HALTED"
    );
    sqlx::query("UPDATE execution_control SET mode = 'ACTIVE' WHERE singleton")
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query(
        "UPDATE execution_strategy_control SET mode = 'REDUCE_ONLY' WHERE strategy_version = $1",
    )
    .bind(CANARY_RISK_VERSION)
    .execute(&pool)
    .await
    .unwrap();
    assert_eq!(
        store
            .account_execution_status("singleton-mainnet-canary")
            .await
            .unwrap()
            .control_mode,
        "REDUCE_ONLY"
    );
    sqlx::query(
        "UPDATE execution_strategy_control SET mode = 'ACTIVE' WHERE strategy_version = $1",
    )
    .bind(CANARY_RISK_VERSION)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE execution_strategy_control SET strategy_manifest_sha256 = $2 WHERE strategy_version = $1",
    )
    .bind(CANARY_RISK_VERSION)
    .bind("f".repeat(64))
    .execute(&pool)
    .await
    .unwrap();
    assert_eq!(
        store
            .account_execution_status("singleton-mainnet-canary")
            .await
            .unwrap()
            .control_mode,
        "HALTED"
    );
    sqlx::query(
        "UPDATE execution_strategy_control SET strategy_manifest_sha256 = $2 WHERE strategy_version = $1",
    )
    .bind(CANARY_RISK_VERSION)
    .bind(execution::BASIS_AAPL_V1_MANIFEST_SHA256)
    .execute(&pool)
    .await
    .unwrap();
    let collision = store
        .intent_status(&IntentStatusRequest {
            intent_id: intent().id,
            payload_sha256: "f".repeat(64),
        })
        .await
        .unwrap();
    assert_eq!(collision.status, "conflict");
    assert!(collision.saga.is_none());
    let legacy_status = store
        .intent_status(&IntentStatusRequest {
            intent_id: legacy_id.into(),
            payload_sha256: "e".repeat(64),
        })
        .await
        .unwrap();
    assert_eq!(legacy_status.status, "unverifiable");
    register_account(
        &pool,
        "account-canary-2",
        "agent-canary-2",
        8,
        "0x0000000000000000000000000000000000000004",
        "0x0000000000000000000000000000000000000005",
        "0x0000000000000000000000000000000000000006",
    )
    .await;
    for snapshot in account_snapshots_for(
        "account-canary-2",
        8,
        "0x0000000000000000000000000000000000000004",
        "0x0000000000000000000000000000000000000005",
        "0x0000000000000000000000000000000000000006",
    ) {
        store.record_account_snapshot(&snapshot).await.unwrap();
    }
    let mut concurrent = intent();
    concurrent.execution_account_id = "account-canary-2".into();
    concurrent.agent_id = "agent-canary-2".into();
    concurrent.lighter_account_index = 8;
    concurrent.robinhood_vault = "0x0000000000000000000000000000000000000004".into();
    concurrent.robinhood_signer = "0x0000000000000000000000000000000000000005".into();
    concurrent.source_evaluation_id =
        "0x7777777777777777777777777777777777777777777777777777777777777777".into();
    concurrent.derive_identifiers().unwrap();
    store.create_intent(&concurrent, 1_200).await.unwrap();
    let active_accounts = sqlx::query_scalar::<_, i64>(
        "SELECT count(DISTINCT execution_account_id) FROM execution_intents WHERE active",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(active_accounts, 2);
    sqlx::query("UPDATE execution_intents SET active = FALSE WHERE id = $1")
        .bind(&concurrent.id)
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query(
        "UPDATE execution_actions SET status = 'failed_safe', completed_at = now() WHERE intent_id = $1",
    )
    .bind(&concurrent.id)
    .execute(&pool)
    .await
    .unwrap();
    store
        .claim_api_nonce("intent", "nnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnn", 1_900_000_000)
        .await
        .unwrap();
    assert!(matches!(
        store
            .claim_api_nonce("intent", "nnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnn", 1_900_000_000,)
            .await,
        Err(StoreError::AuthorizationReplay)
    ));
    store
        .claim_api_nonce(
            "recovery",
            "rrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrr",
            1_900_000_000,
        )
        .await
        .unwrap();
    let venue_event = NewVenueEvent {
        execution_account_id: "singleton-mainnet-canary".into(),
        source: "lighter-auth".into(),
        source_session: "session-1".into(),
        source_event_id: "event-1".into(),
        source_sequence: 1,
        intent_id: intent().id,
        kind: "perp_accepted".into(),
        payload: serde_json::json!({
            "order_id": "order-1",
            "transaction_hash": "0x1111111111111111111111111111111111111111111111111111111111111111",
            "client_order_index": 1,
            "market_index": 101,
            "is_ask": true,
            "reduce_only": false,
            "filled_base": "0",
            "average_price": null
        }),
        publisher_at_ms: 1_200,
        received_at_ms: 1_201,
    };
    let mut cross_tenant_event = venue_event.clone();
    cross_tenant_event.execution_account_id = "account-canary-2".into();
    assert!(matches!(
        store.record_venue_event(&cross_tenant_event).await,
        Err(StoreError::ExecutionAccountUnavailable)
    ));
    assert!(store.record_venue_event(&venue_event).await.unwrap());
    assert!(!store.record_venue_event(&venue_event).await.unwrap());
    let mut restarted_event = venue_event.clone();
    restarted_event.source_session = "session-2".into();
    assert!(store.record_venue_event(&restarted_event).await.unwrap());

    let expected_tx_hash = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    let mut gap_event = venue_event.clone();
    gap_event.source_event_id = "event-gap".into();
    gap_event.source_sequence = 3;
    gap_event.payload["transaction_hash"] = serde_json::json!(expected_tx_hash);
    assert!(store.record_venue_event(&gap_event).await.unwrap());
    let gap_disposition = sqlx::query_scalar::<_, String>(
        r#"
        SELECT route.disposition
        FROM execution_venue_event_routes route
        JOIN execution_venue_events event ON event.id = route.venue_event_id
        WHERE event.source = 'lighter-auth' AND event.source_session = 'session-1'
          AND event.source_event_id = 'event-gap'
        "#,
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(gap_disposition, "quarantined");

    let mut missing_event = venue_event.clone();
    missing_event.source_event_id = "event-2".into();
    missing_event.source_sequence = 2;
    assert!(store.record_venue_event(&missing_event).await.unwrap());
    let healed_frontier = sqlx::query_scalar::<_, i64>(
        "SELECT last_sequence FROM execution_venue_source_sessions \
         WHERE source = 'lighter-auth' AND source_session = 'session-1'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(healed_frontier, 3);
    assert!(!store.record_venue_event(&gap_event).await.unwrap());

    let action = store
        .claim_action("worker-1", std::time::Duration::from_secs(30))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(action.kind, ActionKind::SubmitPerp);
    sqlx::query(
        "UPDATE execution_actions SET lease_expires_at = now() - interval '1 second' WHERE id = $1",
    )
    .bind(&action.id)
    .execute(&pool)
    .await
    .unwrap();
    let reclaimed = store
        .claim_action("worker-1", std::time::Duration::from_secs(30))
        .await
        .unwrap()
        .unwrap();
    assert_ne!(action.lease_token, reclaimed.lease_token);
    assert!(matches!(
        store
            .assign_lighter_nonce(&action.id, "worker-1", &action.lease_token, 7, 4, 11)
            .await,
        Err(StoreError::LeaseLost)
    ));
    let action = reclaimed;
    assert!(matches!(
        store
            .assign_lighter_nonce(&action.id, "worker-1", &action.lease_token, 7, 3, 11)
            .await,
        Err(StoreError::InvalidAction)
    ));
    let nonce = store
        .assign_lighter_nonce(&action.id, "worker-1", &action.lease_token, 7, 4, 11)
        .await
        .unwrap();
    assert_eq!(nonce, 11);
    assert_eq!(
        store
            .assign_lighter_nonce(&action.id, "worker-1", &action.lease_token, 7, 4, 99)
            .await
            .unwrap(),
        11
    );
    store
        .validate_lighter_nonce_binding(&action.id, 7, 4)
        .await
        .unwrap();
    store
        .authorize_entry_send(
            &action.id,
            "worker-1",
            &action.lease_token,
            action.control_version,
            action.account_control_version,
        )
        .await
        .unwrap();
    store.halt("integration halt race").await.unwrap();
    assert!(matches!(
        store
            .authorize_entry_send(
                &action.id,
                "worker-1",
                &action.lease_token,
                action.control_version,
                action.account_control_version,
            )
            .await,
        Err(StoreError::CoordinatorHalted)
    ));
    assert!(matches!(
        store
            .assign_lighter_nonce(&action.id, "worker-1", &action.lease_token, 8, 4, 99)
            .await,
        Err(StoreError::LighterConfigDrift)
    ));
    store
        .record_action_result(
            &action.id,
            "worker-1",
            &action.lease_token,
            "signed",
            serde_json::json!({"tx_hash": "0x01"}),
        )
        .await
        .unwrap();
    let saga = store
        .complete_action(
            &action.id,
            "worker-1",
            &action.lease_token,
            Some(ExecutionEvent::PerpSubmitted),
            serde_json::json!({"accepted": true}),
            Some(NextAction {
                kind: ActionKind::ReconcilePerp,
                key: "reconcile-entry-perp".into(),
                payload: serde_json::json!({"tx_hash": expected_tx_hash}),
            }),
        )
        .await
        .unwrap();
    assert_eq!(saga.state, ExecutionState::PerpSubmitted);
    let action = store
        .claim_action("worker-1", std::time::Duration::from_secs(30))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(action.kind, ActionKind::ReconcilePerp);
    sqlx::query("UPDATE execution_control SET mode = 'ACTIVE', reason = 'tenant isolation test' WHERE singleton")
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query(
        "UPDATE execution_account_control SET mode = 'ACTIVE', reason = 'tenant isolation test' WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE execution_accounts SET status = 'active' WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .execute(&pool)
    .await
    .unwrap();
    for _ in 0..2 {
        store
            .block_action_account(
                &action.id,
                "worker-1",
                &action.lease_token,
                "tenant_repair_test",
                serde_json::json!({"stage": "spot_repair"}),
            )
            .await
            .unwrap();
    }
    let tenant_modes = sqlx::query_as::<_, (String, String, String)>(
        r#"
        SELECT global.mode, account_control.mode, account.status
        FROM execution_account_control account_control
        JOIN execution_accounts account USING (execution_account_id)
        CROSS JOIN execution_control global
        WHERE global.singleton
          AND account.execution_account_id = 'singleton-mainnet-canary'
        "#,
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        tenant_modes,
        ("ACTIVE".into(), "HALTED".into(), "blocked".into())
    );
    let tenant_incidents = sqlx::query_scalar::<_, i64>(
        "SELECT count(*) FROM execution_incidents WHERE intent_id = $1 AND kind = 'tenant_repair_test'",
    )
    .bind(&intent().id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(tenant_incidents, 1);

    sqlx::query(
        "UPDATE execution_account_control SET mode = 'ACTIVE', reason = 'ambiguity test' WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE execution_accounts SET status = 'active' WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .execute(&pool)
    .await
    .unwrap();
    for _ in 0..2 {
        store
            .escalate_reconciliation(
                &action.id,
                "worker-1",
                &action.lease_token,
                "reconciliation_overdue_test",
                serde_json::json!({"deadline_ms": 1_000, "observed_at_ms": 2_000}),
            )
            .await
            .unwrap();
    }
    let ambiguity_modes = sqlx::query_as::<_, (String, String, String)>(
        r#"
        SELECT global.mode, account_control.mode, account.status
        FROM execution_account_control account_control
        JOIN execution_accounts account USING (execution_account_id)
        CROSS JOIN execution_control global
        WHERE global.singleton
          AND account.execution_account_id = 'singleton-mainnet-canary'
        "#,
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        ambiguity_modes,
        ("HALTED".into(), "HALTED".into(), "blocked".into())
    );
    let ambiguity_incidents = sqlx::query_scalar::<_, i64>(
        "SELECT count(*) FROM execution_incidents WHERE intent_id = $1 AND kind = 'reconciliation_overdue_test'",
    )
    .bind(&intent().id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(ambiguity_incidents, 1);
    let routed = store.next_venue_event(&action).await.unwrap().unwrap();
    assert_eq!(routed.payload["transaction_hash"], expected_tx_hash);
    let redelivered = store.next_venue_event(&action).await.unwrap().unwrap();
    assert_eq!(redelivered.id, routed.id);
    let healed_route = sqlx::query_as::<_, (String, String)>(
        "SELECT disposition, reason FROM execution_venue_event_routes WHERE venue_event_id = $1",
    )
    .bind(routed.id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        healed_route,
        ("quarantined".into(), "source_sequence_gap".into())
    );
    let mismatched_routes = sqlx::query_scalar::<_, i64>(
        r#"
        SELECT count(*)
        FROM execution_venue_event_routes route
        JOIN execution_venue_events event ON event.id = route.venue_event_id
        WHERE event.intent_id = $1 AND route.disposition = 'quarantined'
          AND route.reason = 'action_identity_mismatch'
        "#,
    )
    .bind(&intent().id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(mismatched_routes >= 1);

    let saga = store
        .apply_venue_event(
            &action.id,
            "worker-1",
            &action.lease_token,
            routed.id,
            ObservationOutcome {
                transition: None,
                complete: false,
                result: routed.payload,
                next: None,
            },
        )
        .await
        .unwrap();
    assert_eq!(saga.state, ExecutionState::PerpSubmitted);
    sqlx::query("UPDATE execution_actions SET available_at = now() WHERE id = $1")
        .bind(&action.id)
        .execute(&pool)
        .await
        .unwrap();
    let action = store
        .claim_action("worker-1", std::time::Duration::from_secs(30))
        .await
        .unwrap()
        .unwrap();

    store
        .stop_action(
            &action.id,
            "worker-1",
            &action.lease_token,
            coordinator::store::ActionStop::FailedSafe,
            "integration_failure",
            Some(ExecutionEvent::SafeFailure),
            serde_json::json!({}),
        )
        .await
        .unwrap();
    let (active, mode) = sqlx::query_as::<_, (bool, String)>(
        "SELECT intent.active, control.mode FROM execution_intents intent CROSS JOIN execution_control control WHERE intent.id = $1 AND control.singleton",
    )
    .bind(&intent().id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(active);
    assert_eq!(mode, "HALTED");

    let recovery = RecoveryRequest {
        intent_id: intent().id,
        requested_at_ms: 1_300,
        reason: "incident_recovery".into(),
    };
    let recovered = store.request_recovery(&recovery, 1_300).await.unwrap();
    assert_eq!(recovered.state, ExecutionState::PerpSubmitted);
    assert!(matches!(
        store.request_recovery(&recovery, 1_300).await,
        Err(StoreError::Conflict)
    ));
    let recovered_action = sqlx::query_as::<_, (String, String, serde_json::Value)>(
        "SELECT id, kind, payload FROM execution_actions WHERE intent_id = $1 AND action_key LIKE 'recovery-reconcile_perp-%'",
    )
    .bind(&intent().id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(recovered_action.1, "reconcile_perp");
    assert_eq!(recovered_action.2["tx_hash"], expected_tx_hash);
    sqlx::query(
        "UPDATE execution_actions SET status = 'failed_safe', completed_at = now() WHERE id = $1",
    )
    .bind(&recovered_action.0)
    .execute(&pool)
    .await
    .unwrap();

    sqlx::query(
        "UPDATE execution_control SET mode = 'ACTIVE', reason = 'lock test' WHERE singleton",
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE execution_accounts SET status = 'active' WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE execution_account_control SET mode = 'ACTIVE', reason = 'lock test' WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .execute(&pool)
    .await
    .unwrap();
    let mut second = intent();
    second.source_evaluation_id =
        "0x4444444444444444444444444444444444444444444444444444444444444444".into();
    second.client_order_index = 10;
    second.unwind_client_order_index = 20;
    second.derive_identifiers().unwrap();
    let second_result = store.create_intent(&second, 1_200).await;
    assert!(
        matches!(second_result, Err(StoreError::DailyTurnoverExceeded)),
        "unexpected second intent result: {second_result:?}"
    );

    let mut retired = second.clone();
    retired.source_evaluation_id =
        "0x6666666666666666666666666666666666666666666666666666666666666666".into();
    retired.client_order_index = 30;
    retired.unwind_client_order_index = 40;
    retired.derive_identifiers().unwrap();
    let mut retirement = pool.begin().await.unwrap();
    sqlx::query("SELECT pg_advisory_xact_lock(hashtext($1))")
        .bind(CANARY_RISK_VERSION)
        .execute(&mut *retirement)
        .await
        .unwrap();
    sqlx::query(
        "INSERT INTO execution_promotion_events \
         (strategy_version, from_state, to_state, evidence_sha256, approved_by) \
         VALUES ($1, $2, $3, $4, $5)",
    )
    .bind(CANARY_RISK_VERSION)
    .bind("canary_eligible")
    .bind("retired")
    .bind(&digest)
    .bind("approval-record")
    .execute(&mut *retirement)
    .await
    .unwrap();
    let admission_store = store.clone();
    let admission_intent = retired.clone();
    let mut admission = tokio::spawn(async move {
        admission_store
            .create_intent(&admission_intent, 1_200)
            .await
    });
    assert!(
        tokio::time::timeout(std::time::Duration::from_millis(100), &mut admission)
            .await
            .is_err()
    );
    retirement.commit().await.unwrap();
    assert!(matches!(
        admission.await.unwrap(),
        Err(StoreError::Promotion(_))
    ));

    let mut conflicting_event = venue_event;
    conflicting_event.payload["order_id"] = serde_json::json!("different-order");
    assert!(matches!(
        store.record_venue_event(&conflicting_event).await,
        Err(StoreError::VenueEventConflict)
    ));
    sqlx::query(
        "UPDATE execution_control SET mode = 'ACTIVE', reason = 'exit test' WHERE singleton",
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE execution_accounts SET status = 'active' WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE execution_account_control SET mode = 'ACTIVE', reason = 'exit test' \
         WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .execute(&pool)
    .await
    .unwrap();

    let mut hedged = ExecutionSaga::new(intent().id).unwrap();
    for event in [
        ExecutionEvent::PrecheckPassed,
        ExecutionEvent::PerpSubmitted,
        ExecutionEvent::PerpFilled {
            filled_base: 1_000_000,
        },
        ExecutionEvent::SpotSubmitted,
        ExecutionEvent::SpotConfirmed {
            received_raw: 2_000_000,
        },
    ] {
        hedged.apply(event).unwrap();
    }
    sqlx::query(
        "UPDATE execution_intents SET saga = $2, saga_version = $3, active = TRUE WHERE id = $1",
    )
    .bind(&intent().id)
    .bind(sqlx::types::Json(&hedged))
    .bind(i64::try_from(hedged.version).unwrap())
    .execute(&pool)
    .await
    .unwrap();
    let exit_quote = NewMarketQuote {
        source: "execution-authority".into(),
        source_session: "exit-quote-session-1".into(),
        source_event_id: "exit-quote-1".into(),
        source_sequence: 1,
        execution_account_id: Some("singleton-mainnet-canary".into()),
        market_manifest: intent().evidence.market_manifest,
        strategy_manifest_sha256: Some(BASIS_AAPL_V1_MANIFEST_SHA256.into()),
        target_strategy_manifest_sha256: Some(intent().strategy_manifest_sha256),
        route_sha256: Some(BASIS_AAPL_V1_ROUTE_SHA256.into()),
        lighter_market_index: Some(101),
        quote_block_hash: "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
            .into(),
        mark_price: 25_000,
        expected_ui_multiplier: intent().expected_ui_multiplier.to_string(),
        min_oracle_round_id: intent().min_oracle_round_id.to_string(),
        publisher_at_ms: 1_099,
        received_at_ms: 1_100,
        expires_at_ms: 30_000,
        intent_id: Some(intent().id),
        spot_unwind_amount_in: Some("2000000".into()),
        spot_unwind_expected_amount_out: Some("25000000".into()),
        unwind_phase: Some("perp_and_spot".into()),
        perp_unwind_base_amount: Some(1_000_000),
        perp_unwind_limit_price: Some(30_000),
        submission_deadline_ms: Some(30_000),
        reconciliation_deadline_ms: Some(90_000),
    };
    let exit_quote_receipt = store.record_market_quote(&exit_quote).await.unwrap();
    assert!(exit_quote_receipt.created);
    let exit_request = ExitRequest {
        request_id: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa".into(),
        execution_account_id: "singleton-mainnet-canary".into(),
        intent_id: intent().id,
        quote_source_session: "exit-quote-session-1".into(),
        quote_source_event_id: "exit-quote-1".into(),
        quote_payload_sha256: exit_quote_receipt.receipt.payload_sha256,
        perp_unwind_price: 30_000,
        minimum_unwind_settlement_out: "24000000".into(),
        requested_at_ms: 1_200,
        submission_deadline_ms: 30_000,
        reconciliation_deadline_ms: 90_000,
        reason: "strategy_exit".into(),
    };
    let mut unmarketable = exit_request.clone();
    unmarketable.perp_unwind_price = 24_999;
    assert!(matches!(
        store.request_exit(&unmarketable, 1_200).await,
        Err(StoreError::MarketEvidenceMismatch)
    ));
    let mut excessive_slippage = exit_request.clone();
    excessive_slippage.minimum_unwind_settlement_out = "23000000".into();
    assert!(matches!(
        store.request_exit(&excessive_slippage, 1_200).await,
        Err(StoreError::MarketEvidenceMismatch)
    ));
    let exiting = store.request_exit(&exit_request, 1_200).await.unwrap();
    assert!(exiting.created);
    assert_eq!(exiting.saga.state, ExecutionState::Unwinding);
    let duplicate_exit = store.request_exit(&exit_request, 9_999_999).await.unwrap();
    assert!(!duplicate_exit.created);
    assert_eq!(duplicate_exit.payload_sha256, exiting.payload_sha256);
    let exit_status = store
        .exit_status(&ExitStatusRequest {
            request_id: exit_request.request_id.clone(),
            payload_sha256: exiting.payload_sha256.clone(),
        })
        .await
        .unwrap();
    assert_eq!(exit_status.status, "persisted");
    assert_eq!(exit_status.saga, Some(exiting.saga.clone()));
    let exit_action = sqlx::query_as::<_, (String, String, serde_json::Value)>(
        "SELECT id, kind, payload FROM execution_actions WHERE intent_id = $1 AND action_key LIKE 'exit-perp-%'",
    )
    .bind(&intent().id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(exit_action.1, "unwind_perp");
    assert_eq!(
        exit_action.2["exit_authority"]["submission_deadline_ms"],
        30_000
    );
    assert_eq!(exit_action.2["unwind_attempt"], 0);

    let mut recovery_quote = exit_quote.clone();
    recovery_quote.source_event_id = "exit-quote-2".into();
    recovery_quote.source_sequence = 2;
    recovery_quote.quote_block_hash =
        "0xabababababababababababababababababababababababababababababababab".into();
    recovery_quote.publisher_at_ms = 1_299;
    recovery_quote.received_at_ms = 1_300;
    recovery_quote.expires_at_ms = 31_000;
    recovery_quote.submission_deadline_ms = Some(recovery_quote.expires_at_ms);
    let recovery_quote_receipt = store.record_market_quote(&recovery_quote).await.unwrap();
    assert!(recovery_quote_receipt.created);
    let mut recovery_request = exit_request.clone();
    recovery_request.request_id =
        "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb".into();
    recovery_request.quote_source_event_id = "exit-quote-2".into();
    recovery_request.quote_payload_sha256 = recovery_quote_receipt.receipt.payload_sha256;
    recovery_request.requested_at_ms = 1_400;
    recovery_request.submission_deadline_ms = 31_000;
    assert!(matches!(
        store.request_exit(&recovery_request, 1_400).await,
        Err(StoreError::Conflict)
    ));

    sqlx::query(
        "UPDATE execution_actions SET status = 'failed_safe', completed_at = now() WHERE id = $1",
    )
    .bind(&exit_action.0)
    .execute(&pool)
    .await
    .unwrap();
    store.request_exit(&recovery_request, 1_400).await.unwrap();
    let recovery_action = sqlx::query_as::<_, (String, i16)>(
        "SELECT id, (payload->>'unwind_attempt')::smallint FROM execution_actions WHERE intent_id = $1 AND id <> $2 AND kind = 'unwind_perp' ORDER BY created_at DESC LIMIT 1",
    )
    .bind(&intent().id)
    .bind(&exit_action.0)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(recovery_action.1, 1);
    sqlx::query(
        "UPDATE execution_actions SET status = 'failed_safe', completed_at = now() WHERE id = $1",
    )
    .bind(&recovery_action.0)
    .execute(&pool)
    .await
    .unwrap();

    let mut perp_only = ExecutionSaga::new(intent().id).unwrap();
    for event in [
        ExecutionEvent::PrecheckPassed,
        ExecutionEvent::PerpSubmitted,
        ExecutionEvent::PerpFilled {
            filled_base: 1_000_000,
        },
    ] {
        perp_only.apply(event).unwrap();
    }
    sqlx::query(
        "UPDATE execution_intents SET saga = $2, saga_version = $3, active = TRUE WHERE id = $1",
    )
    .bind(&intent().id)
    .bind(sqlx::types::Json(&perp_only))
    .bind(i64::try_from(perp_only.version).unwrap())
    .execute(&pool)
    .await
    .unwrap();
    let zero_spot_quote = NewMarketQuote {
        source: "execution-authority".into(),
        source_session: "exit-quote-session-2".into(),
        source_event_id: "exit-quote-3".into(),
        source_sequence: 1,
        execution_account_id: Some("singleton-mainnet-canary".into()),
        market_manifest: intent().evidence.market_manifest,
        strategy_manifest_sha256: Some(BASIS_AAPL_V1_MANIFEST_SHA256.into()),
        target_strategy_manifest_sha256: Some(intent().strategy_manifest_sha256),
        route_sha256: Some(BASIS_AAPL_V1_ROUTE_SHA256.into()),
        lighter_market_index: Some(101),
        quote_block_hash: "0xcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
            .into(),
        mark_price: 25_000,
        expected_ui_multiplier: intent().expected_ui_multiplier.to_string(),
        min_oracle_round_id: intent().min_oracle_round_id.to_string(),
        publisher_at_ms: 1_499,
        received_at_ms: 1_500,
        expires_at_ms: 31_500,
        intent_id: Some(intent().id),
        spot_unwind_amount_in: Some("0".into()),
        spot_unwind_expected_amount_out: Some("0".into()),
        unwind_phase: Some("perp_and_spot".into()),
        perp_unwind_base_amount: Some(1_000_000),
        perp_unwind_limit_price: Some(30_000),
        submission_deadline_ms: Some(31_500),
        reconciliation_deadline_ms: Some(91_500),
    };
    let zero_quote_receipt = store.record_market_quote(&zero_spot_quote).await.unwrap();
    assert!(zero_quote_receipt.created);
    let perp_only_exit = ExitRequest {
        request_id: "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc".into(),
        execution_account_id: "singleton-mainnet-canary".into(),
        intent_id: intent().id,
        quote_source_session: zero_spot_quote.source_session.clone(),
        quote_source_event_id: zero_spot_quote.source_event_id.clone(),
        quote_payload_sha256: zero_quote_receipt.receipt.payload_sha256,
        perp_unwind_price: 30_000,
        minimum_unwind_settlement_out: "0".into(),
        requested_at_ms: 1_600,
        submission_deadline_ms: 31_500,
        reconciliation_deadline_ms: 91_500,
        reason: "operator_exit".into(),
    };
    let unwinding = store.request_exit(&perp_only_exit, 1_600).await.unwrap();
    assert_eq!(unwinding.saga.state, ExecutionState::Unwinding);
    let bounded_action = sqlx::query_as::<_, (String, serde_json::Value)>(
        "SELECT id, payload FROM execution_actions WHERE intent_id = $1 AND kind = 'unwind_perp' AND status = 'pending' ORDER BY created_at DESC LIMIT 1",
    )
    .bind(&intent().id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(bounded_action.1["unwind_attempt"], 2);
    assert_eq!(bounded_action.1["client_order_index"], 4);
    assert_eq!(bounded_action.1["exit_authority"]["spot_amount_in"], "0");
    sqlx::query(
        "UPDATE execution_actions SET status = 'failed_safe', completed_at = now() WHERE id = $1",
    )
    .bind(&bounded_action.0)
    .execute(&pool)
    .await
    .unwrap();

    let mut operator_quote = zero_spot_quote.clone();
    operator_quote.source_event_id = "exit-quote-4".into();
    operator_quote.source_sequence = 2;
    operator_quote.quote_block_hash =
        "0xdededededededededededededededededededededededededededededededede".into();
    operator_quote.publisher_at_ms = 1_699;
    operator_quote.received_at_ms = 1_700;
    operator_quote.expires_at_ms = 31_700;
    operator_quote.submission_deadline_ms = Some(operator_quote.expires_at_ms);
    operator_quote.reconciliation_deadline_ms = Some(91_700);
    let operator_quote_receipt = store.record_market_quote(&operator_quote).await.unwrap();
    assert!(operator_quote_receipt.created);
    let mut operator_exit = perp_only_exit.clone();
    operator_exit.request_id =
        "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd".into();
    operator_exit.quote_source_event_id = operator_quote.source_event_id.clone();
    operator_exit.quote_payload_sha256 = operator_quote_receipt.receipt.payload_sha256;
    operator_exit.requested_at_ms = 1_800;
    operator_exit.submission_deadline_ms = 31_700;
    operator_exit.reconciliation_deadline_ms = 91_700;
    store.request_exit(&operator_exit, 1_800).await.unwrap();
    let operator_action = sqlx::query_as::<_, (String, serde_json::Value)>(
        "SELECT id, payload FROM execution_actions WHERE intent_id = $1 AND kind = 'unwind_perp' AND status = 'pending' ORDER BY created_at DESC LIMIT 1",
    )
    .bind(&intent().id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(operator_action.1["operator_recovery"], true);
    let operator_index = operator_action.1["client_order_index"].as_u64().unwrap();
    assert!(operator_index >= 1_099_511_627_776);
    assert!(operator_action.1.get("unwind_attempt").is_none());
    let owner = sqlx::query_scalar::<_, String>(
        "SELECT intent_id FROM execution_identifiers WHERE namespace = 'lighter_client_order' AND value = $1",
    )
    .bind(operator_index.to_string())
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(owner, intent().id);

    sqlx::query(
        "UPDATE execution_actions SET status = 'failed_safe', completed_at = now() WHERE id = $1",
    )
    .bind(&operator_action.0)
    .execute(&pool)
    .await
    .unwrap();
    let control_action_id = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee";
    sqlx::query(
        r#"
        INSERT INTO execution_actions (id, intent_id, kind, action_key, payload, status)
        VALUES ($1, $2, 'unwind_perp', 'control-command-pause-live-unwind-perp', $3, 'pending')
        "#,
    )
    .bind(control_action_id)
    .bind(intent().id)
    .bind(sqlx::types::Json(serde_json::json!({
        "filled_base": 1_000_000,
        "unwound_before": 0,
        "exit_reason": "operator_exit",
        "control_command_id": "command-pause-live"
    })))
    .execute(&pool)
    .await
    .unwrap();
    let mut pause_quote = operator_quote.clone();
    pause_quote.source_event_id = "exit-quote-pause".into();
    pause_quote.source_sequence = 3;
    pause_quote.quote_block_hash =
        "0xefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefef".into();
    pause_quote.publisher_at_ms = 1_899;
    pause_quote.received_at_ms = 1_900;
    pause_quote.expires_at_ms = 31_900;
    pause_quote.submission_deadline_ms = Some(31_900);
    pause_quote.reconciliation_deadline_ms = Some(91_900);
    let pause_quote_receipt = store.record_market_quote(&pause_quote).await.unwrap();
    let mut pause_exit = operator_exit.clone();
    pause_exit.request_id =
        "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee".into();
    pause_exit.quote_source_event_id = pause_quote.source_event_id.clone();
    pause_exit.quote_payload_sha256 = pause_quote_receipt.receipt.payload_sha256;
    pause_exit.requested_at_ms = 2_000;
    pause_exit.submission_deadline_ms = 31_900;
    pause_exit.reconciliation_deadline_ms = 91_900;
    let bound_pause = store.request_exit(&pause_exit, 2_000).await.unwrap();
    assert_eq!(bound_pause.saga.state, ExecutionState::Unwinding);
    let bound_authority = sqlx::query_scalar::<_, serde_json::Value>(
        "SELECT payload->'exit_authority' FROM execution_actions WHERE id = $1",
    )
    .bind(control_action_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(bound_authority["quote_source_event_id"], "exit-quote-pause");
    sqlx::query(
        "UPDATE execution_actions SET status = 'failed_safe', completed_at = now() WHERE id = $1",
    )
    .bind(control_action_id)
    .execute(&pool)
    .await
    .unwrap();

    register_account(
        &pool,
        "account-control-test",
        "agent-control-test",
        11,
        "0x0000000000000000000000000000000000000012",
        "0x0000000000000000000000000000000000000013",
        "0x0000000000000000000000000000000000000014",
    )
    .await;
    let mut snapshots = account_snapshots_for(
        "account-control-test",
        11,
        "0x0000000000000000000000000000000000000012",
        "0x0000000000000000000000000000000000000013",
        "0x0000000000000000000000000000000000000014",
    );
    for snapshot in &mut snapshots {
        set_snapshot_times(snapshot, 4_000, 4_999, 8_000);
        store.record_account_snapshot(snapshot).await.unwrap();
    }
    let command = |command_id: &str, command: &str, requested_at_ms| AccountCommandRequest {
        command_id: command_id.into(),
        execution_account_id: "account-control-test".into(),
        agent_id: "agent-control-test".into(),
        command: command.into(),
        requested_at_ms,
    };
    let paused = store
        .submit_account_command(&command("command-pause-control", "pause", 5_000), 5_000)
        .await
        .unwrap();
    assert_eq!(paused.status, "completed");
    assert_eq!(paused.control_mode, "REDUCE_ONLY");
    let closed = store
        .submit_account_command(&command("command-close-control", "close", 5_100), 5_100)
        .await
        .unwrap();
    assert_eq!(closed.status, "awaiting_owner_signature");
    assert!(closed.reconciled_flat);
    assert!(!closed.agent_revoked);
    assert_eq!(closed.owner_actions.len(), 1);
    assert_eq!(closed.owner_actions[0].data, "0x51755334");

    for snapshot in &mut snapshots {
        snapshot.source_sequence = 2;
        set_snapshot_times(snapshot, 5_000, 5_199, 8_000);
        if snapshot.source == "robinhood-chain" {
            snapshot.payload["agent_enabled"] = serde_json::json!(false);
            snapshot.payload["risk_mode"] = serde_json::json!("HALTED");
        }
        store.record_account_snapshot(snapshot).await.unwrap();
    }
    let latest_only = store
        .account_command_status(
            &AccountCommandStatusRequest {
                command_id: "command-close-control".into(),
                execution_account_id: "account-control-test".into(),
            },
            5_200,
        )
        .await
        .unwrap();
    assert_eq!(latest_only.status, "awaiting_owner_signature");
    assert!(!latest_only.agent_revoked);
    assert_eq!(latest_only.owner_actions.len(), 1);

    for snapshot in &mut snapshots {
        snapshot.source_sequence = 3;
        set_snapshot_times(snapshot, 5_000, 5_299, 8_000);
        if snapshot.source == "robinhood-chain" {
            snapshot.payload["finalized_agent_address"] =
                serde_json::json!("0x0000000000000000000000000000000000000000");
            snapshot.payload["finalized_agent_enabled"] = serde_json::json!(false);
            snapshot.payload["finalized_agent_revoked"] = serde_json::json!(true);
            snapshot.payload["finalized_risk_mode"] = serde_json::json!("HALTED");
        }
        store.record_account_snapshot(snapshot).await.unwrap();
    }
    let closed = store
        .account_command_status(
            &AccountCommandStatusRequest {
                command_id: "command-close-control".into(),
                execution_account_id: "account-control-test".into(),
            },
            5_300,
        )
        .await
        .unwrap();
    assert_eq!(closed.status, "completed");
    assert_eq!(closed.control_mode, "HALTED");
    assert!(closed.agent_revoked);
    let withdrawal = store
        .submit_account_command(
            &command("command-withdraw-control", "withdraw", 5_300),
            5_300,
        )
        .await
        .unwrap();
    assert_eq!(withdrawal.status, "awaiting_owner_signature");
    assert_eq!(withdrawal.owner_actions.len(), 1);
    assert_eq!(
        withdrawal.owner_actions[0].from,
        "0x0000000000000000000000000000000000000014"
    );
    assert!(withdrawal.owner_actions[0].data.starts_with("0x142834dd"));

    for snapshot in &mut snapshots {
        snapshot.source_sequence = 4;
        set_snapshot_times(snapshot, 5_000, 5_399, 8_000);
        if snapshot.source == "robinhood-chain" {
            snapshot.payload["settlement_balance_raw"] = serde_json::json!("0");
        }
        store.record_account_snapshot(snapshot).await.unwrap();
    }
    let withdrawal = store
        .account_command_status(
            &AccountCommandStatusRequest {
                command_id: "command-withdraw-control".into(),
                execution_account_id: "account-control-test".into(),
            },
            5_400,
        )
        .await
        .unwrap();
    assert_eq!(withdrawal.status, "completed");
    assert!(withdrawal.owner_actions.is_empty());

    sqlx::query(
        r#"
        UPDATE execution_accounts
        SET status = 'closed'
        WHERE execution_account_id IN (
            SELECT execution_account_id FROM execution_account_registrations
        )
        "#,
    )
    .execute(&pool)
    .await
    .unwrap();
    let first_registration = registration(
        "registry-account-one",
        "registry-agent-one",
        71,
        "0x0000000000000000000000000000000000000021",
        "0x0000000000000000000000000000000000000022",
        "0x0000000000000000000000000000000000000023",
    );
    let first = store
        .register_execution_account(&first_registration)
        .await
        .unwrap();
    assert!(first.created);
    assert_eq!(first.response.account_status, "active");
    assert_eq!(first.response.control_mode, "REDUCE_ONLY");
    assert_eq!(
        first.response.readiness,
        coordinator::store::AccountRegistrationReadiness {
            venue_approved: false,
            oracle_healthy: false,
            sequencer_healthy: false,
            reconciliation_ready: false,
            exit_authority_ready: false,
            alerting_ready: true,
            safe_rotation_ready: true,
        }
    );
    let retry = store
        .register_execution_account(&first_registration)
        .await
        .unwrap();
    assert!(!retry.created);
    assert_eq!(retry.response, first.response);
    store
        .claim_api_nonce(
            "account_registration",
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            1_900_000_000,
        )
        .await
        .unwrap();
    assert!(matches!(
        store
            .claim_api_nonce(
                "account_registration",
                "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                1_900_000_000,
            )
            .await,
        Err(StoreError::AuthorizationReplay)
    ));

    let second_registration = registration(
        "registry-account-two",
        "registry-agent-two",
        72,
        "0x0000000000000000000000000000000000000024",
        "0x0000000000000000000000000000000000000025",
        "0x0000000000000000000000000000000000000026",
    );
    assert!(matches!(
        store.register_execution_account(&second_registration).await,
        Err(StoreError::AccountCapacityExceeded)
    ));
    sqlx::query("UPDATE execution_accounts SET status = 'closed' WHERE execution_account_id = $1")
        .bind(&first_registration.execution_account_id)
        .execute(&pool)
        .await
        .unwrap();
    store
        .register_execution_account(&second_registration)
        .await
        .unwrap();
    let second = store
        .execution_account_registration("registry-account-two")
        .await
        .unwrap();
    assert_eq!(second.binding_sha256, second_registration.binding_sha256);
    assert_eq!(second.control_mode, "REDUCE_ONLY");

    sqlx::query("UPDATE execution_accounts SET status = 'closed' WHERE execution_account_id = $1")
        .bind(&second_registration.execution_account_id)
        .execute(&pool)
        .await
        .unwrap();
    let concurrent_one = registration(
        "registry-race-one",
        "registry-race-agent-one",
        73,
        "0x0000000000000000000000000000000000000031",
        "0x0000000000000000000000000000000000000032",
        "0x0000000000000000000000000000000000000033",
    );
    let concurrent_two = registration(
        "registry-race-two",
        "registry-race-agent-two",
        74,
        "0x0000000000000000000000000000000000000034",
        "0x0000000000000000000000000000000000000035",
        "0x0000000000000000000000000000000000000036",
    );
    let (first_race, second_race) = tokio::join!(
        store.register_execution_account(&concurrent_one),
        store.register_execution_account(&concurrent_two),
    );
    assert!(matches!(
        (&first_race, &second_race),
        (Ok(_), Err(StoreError::AccountCapacityExceeded))
            | (Err(StoreError::AccountCapacityExceeded), Ok(_))
    ));

    let mutation = sqlx::query(
        "UPDATE execution_accounts SET robinhood_signer = $2 WHERE execution_account_id = $1",
    )
    .bind("registry-account-one")
    .bind("0x0000000000000000000000000000000000000029")
    .execute(&pool)
    .await;
    assert!(mutation.is_err());

    let substituted = registration(
        "registry-account-three",
        "registry-agent-three",
        71,
        "0x0000000000000000000000000000000000000027",
        "0x0000000000000000000000000000000000000028",
        "0x0000000000000000000000000000000000000026",
    );
    assert!(matches!(
        store.register_execution_account(&substituted).await,
        Err(StoreError::AccountRegistrationConflict)
    ));
    let halted = sqlx::query_scalar::<_, i64>(
        r#"
        SELECT count(*)
        FROM execution_account_control
        WHERE execution_account_id IN ('registry-account-one', 'registry-account-two')
          AND mode = 'HALTED'
        "#,
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(halted, 2);
    let global_mode =
        sqlx::query_scalar::<_, String>("SELECT mode FROM execution_control WHERE singleton")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(global_mode, "HALTED");
    let incidents = sqlx::query_scalar::<_, i64>(
        r#"
        SELECT count(*)
        FROM execution_incidents
        WHERE kind = 'account_registration_identity_conflict'
          AND execution_account_id IN ('registry-account-one', 'registry-account-two')
        "#,
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(incidents, 2);
    assert!(matches!(
        store
            .execution_account_registration("registry-account-three")
            .await,
        Err(StoreError::AccountRegistrationMissing)
    ));

    sqlx::query("UPDATE execution_control SET mode = 'ACTIVE' WHERE singleton")
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query(
        "UPDATE execution_account_control SET mode = 'ACTIVE' WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE execution_accounts SET status = 'active' WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .execute(&pool)
    .await
    .unwrap();
    let mut unreviewed_market_quote = exit_quote.clone();
    unreviewed_market_quote.source_event_id = "unreviewed-market-quote".into();
    unreviewed_market_quote.source_sequence = 98;
    unreviewed_market_quote.lighter_market_index = Some(102);
    assert!(matches!(
        store.record_market_quote(&unreviewed_market_quote).await,
        Err(StoreError::ExecutionAccountUnavailable)
    ));

    let mut cross_account_quote = exit_quote.clone();
    cross_account_quote.source_event_id = "cross-account-quote".into();
    cross_account_quote.source_sequence = 99;
    cross_account_quote.execution_account_id = Some("account-canary-2".into());
    assert!(matches!(
        store.record_market_quote(&cross_account_quote).await,
        Err(StoreError::ExecutionAccountUnavailable)
    ));

    let mut colliding_exit = exit_request.clone();
    colliding_exit.reason = "operator_exit".into();
    assert!(matches!(
        store.request_exit(&colliding_exit, 9_999_999).await,
        Err(StoreError::ExitPayloadConflict)
    ));
    let exit_payload_incidents = sqlx::query_scalar::<_, i64>(
        "SELECT count(*) FROM execution_incidents WHERE kind = 'exit_payload_identity_conflict' AND intent_id = $1",
    )
    .bind(intent().id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(exit_payload_incidents, 1);

    sqlx::query("UPDATE execution_control SET mode = 'ACTIVE' WHERE singleton")
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query(
        "UPDATE execution_account_control SET mode = 'ACTIVE' WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE execution_accounts SET status = 'active' WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .execute(&pool)
    .await
    .unwrap();
    let mut colliding_quote = exit_quote.clone();
    colliding_quote.mark_price += 1;
    assert!(matches!(
        store.record_market_quote(&colliding_quote).await,
        Err(StoreError::MarketQuoteConflict)
    ));
    let quote_payload_incidents = sqlx::query_scalar::<_, i64>(
        "SELECT count(*) FROM execution_incidents WHERE kind = 'market_quote_identity_conflict' AND execution_account_id = 'singleton-mainnet-canary'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(quote_payload_incidents, 1);

    sqlx::query("UPDATE execution_control SET mode = 'ACTIVE' WHERE singleton")
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query(
        "UPDATE execution_account_control SET mode = 'ACTIVE' WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query("UPDATE execution_intents SET payload_sha256 = repeat('f', 64) WHERE id = $1")
        .bind(intent().id)
        .execute(&pool)
        .await
        .unwrap();
    assert!(matches!(
        store.create_intent(&intent(), 9_999_999).await,
        Err(StoreError::IntentPayloadConflict)
    ));
    let global_mode =
        sqlx::query_scalar::<_, String>("SELECT mode FROM execution_control WHERE singleton")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(global_mode, "HALTED");
    let account_mode = sqlx::query_scalar::<_, String>(
        "SELECT mode FROM execution_account_control WHERE execution_account_id = 'singleton-mainnet-canary'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(account_mode, "HALTED");
    let payload_incidents = sqlx::query_scalar::<_, i64>(
        "SELECT count(*) FROM execution_incidents WHERE kind = 'intent_payload_identity_conflict' AND intent_id = $1",
    )
    .bind(intent().id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(payload_incidents, 1);
}

#[tokio::test]
#[ignore = "requires a disposable PostgreSQL database"]
async fn prior_manifest_registration_does_not_consume_current_canary_capacity() {
    let url = std::env::var("TEST_DATABASE_URL").expect("TEST_DATABASE_URL is required");
    let pool = PgPool::connect(&url).await.unwrap();
    for migration in [
        include_str!("../migrations/0001_execution.sql"),
        include_str!("../migrations/0002_execution_actions.sql"),
        include_str!("../migrations/0003_venue_event_binding.sql"),
        include_str!("../migrations/0004_market_authority.sql"),
        include_str!("../migrations/0005_exit_authority.sql"),
        include_str!("../migrations/0006_multi_account_execution.sql"),
        include_str!("../migrations/0007_account_commands.sql"),
        include_str!("../migrations/0008_account_registration.sql"),
        include_str!("../migrations/0009_intent_idempotency.sql"),
        include_str!("../migrations/0010_exit_dispatch.sql"),
        include_str!("../migrations/0011_operator_restrictions.sql"),
        include_str!("../migrations/0012_internal_canary_promotion.sql"),
        include_str!("../migrations/0013_derived_canary_readiness.sql"),
        include_str!("../migrations/0014_open_episode_resolution.sql"),
        include_str!("../migrations/0015_exit_execution_policy.sql"),
        include_str!("../migrations/0016_enable_basis_aapl_canary.sql"),
        include_str!("../migrations/0017_refresh_basis_aapl_canary.sql"),
        include_str!("../../runtime/live-scheduler/migrations/0001_live_scheduler.sql"),
        include_str!("../../runtime/live-scheduler/migrations/0002_natural_strategy_exit.sql"),
        include_str!("../../runtime/live-scheduler/migrations/0003_repin_strategy_manifest.sql"),
    ] {
        sqlx::raw_sql(migration).execute(&pool).await.unwrap();
    }

    let mut prior = registration(
        "prior-basis-account",
        "prior-basis-agent",
        81,
        "0x0000000000000000000000000000000000000081",
        "0x0000000000000000000000000000000000000082",
        "0x0000000000000000000000000000000000000083",
    );
    prior.strategy_manifest_sha256 = PRIOR_BASIS_AAPL_V1_MANIFEST_SHA256.into();
    prior.binding_sha256 = prior.calculate_binding_sha256();
    sqlx::query(
        r#"
        INSERT INTO execution_accounts (
            execution_account_id, agent_id, strategy_version, risk_version, status,
            lighter_account_index, lighter_api_key_index, robinhood_vault,
            robinhood_signer, owner_address, strategy_manifest_sha256, binding_sha256
        ) VALUES ($1, $2, $3, $4, 'active', $5, $6, $7, $8, $9, $10, $11)
        "#,
    )
    .bind(&prior.execution_account_id)
    .bind(&prior.agent_id)
    .bind(&prior.strategy_version)
    .bind(&prior.risk_version)
    .bind(prior.lighter_account_index)
    .bind(prior.lighter_api_key_index)
    .bind(&prior.robinhood_vault)
    .bind(&prior.robinhood_signer)
    .bind(&prior.robinhood_owner)
    .bind(&prior.strategy_manifest_sha256)
    .bind(&prior.binding_sha256)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
        INSERT INTO execution_account_registrations (
            execution_account_id, agent_id, strategy_version, risk_version,
            strategy_manifest_sha256, lighter_account_index, lighter_api_key_index,
            robinhood_owner, robinhood_vault, robinhood_signer, binding_sha256
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
        "#,
    )
    .bind(&prior.execution_account_id)
    .bind(&prior.agent_id)
    .bind(&prior.strategy_version)
    .bind(&prior.risk_version)
    .bind(&prior.strategy_manifest_sha256)
    .bind(prior.lighter_account_index)
    .bind(prior.lighter_api_key_index)
    .bind(&prior.robinhood_owner)
    .bind(&prior.robinhood_vault)
    .bind(&prior.robinhood_signer)
    .bind(&prior.binding_sha256)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO execution_account_control (execution_account_id, mode, reason) \
         VALUES ($1, 'ACTIVE', 'prior release')",
    )
    .bind(&prior.execution_account_id)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query("INSERT INTO execution_account_readiness (execution_account_id) VALUES ($1)")
        .bind(&prior.execution_account_id)
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query(
        r#"
        INSERT INTO execution_strategy_control (
            strategy_version, strategy_manifest_sha256, mode, reason
        ) VALUES ($1, $2, 'ACTIVE', 'prior release')
        "#,
    )
    .bind(&prior.strategy_version)
    .bind(&prior.strategy_manifest_sha256)
    .execute(&pool)
    .await
    .unwrap();

    let scheduler_evaluation_id =
        "0x8181818181818181818181818181818181818181818181818181818181818181";
    sqlx::query(
        r#"
        INSERT INTO live_scheduler_approvals (
            evaluation_id, execution_account_id, agent_id, evaluation, readiness,
            account_state, approval_sha256, expires_at
        ) VALUES ($1, $2, $3, $4, $5, $6, repeat('8', 64), now() + interval '1 day')
        "#,
    )
    .bind(scheduler_evaluation_id)
    .bind(&prior.execution_account_id)
    .bind(&prior.agent_id)
    .bind(serde_json::json!({
        "id": scheduler_evaluation_id,
        "strategy_version": "basis-aapl-v1",
        "strategy_manifest_sha256": PRIOR_BASIS_AAPL_V1_MANIFEST_SHA256,
        "source_config_sha256":
            "59106a18758a95af45e6ac1a8257843cfbd2a45fd09b5b3c3f429d3dedb56c2a",
        "dataset_manifest":
            "0x8181818181818181818181818181818181818181818181818181818181818181",
        "market_manifest":
            "0x8282828282828282828282828282828282828282828282828282828282828282",
        "status": "approved",
        "action": "entry",
        "observed_at_ms": 1,
        "estimated_cost_micros": 1,
        "source_episode_id": "81818181-8181-4181-8181-818181818181",
        "paper_evaluation_id": "82828282-8282-4282-8282-828282828282",
        "pair_intent_id": "",
    }))
    .bind(serde_json::json!({
        "execution_account_id": prior.execution_account_id,
        "agent_id": prior.agent_id,
        "strategy_version": "basis-aapl-v1",
        "strategy_manifest_sha256": PRIOR_BASIS_AAPL_V1_MANIFEST_SHA256,
        "lifecycle": "running",
        "global_control": "ACTIVE",
        "strategy_control": "ACTIVE",
        "account_control": "ACTIVE",
        "fully_verified": true,
        "vault_wired": true,
        "vault_funded": true,
        "execution_signer_funded": true,
        "lighter_linked": true,
        "lighter_funded": true,
        "route_healthy": true,
        "oracle_healthy": true,
        "sequencer_healthy": true,
        "observed_at_ms": 1,
    }))
    .bind(serde_json::json!({
        "execution_account_id": prior.execution_account_id,
        "agent_id": prior.agent_id,
        "strategy_manifest_sha256": PRIOR_BASIS_AAPL_V1_MANIFEST_SHA256,
        "lighter_account_index": prior.lighter_account_index,
        "lighter_api_key_index": prior.lighter_api_key_index,
        "lighter_market_index": 101,
        "lighter_nonce_aligned": true,
        "unknown_lighter_orders": 0,
        "unknown_lighter_positions": 0,
        "collateral_micros": 100_000_000,
        "maintenance_margin_micros": 25_000_000,
        "robinhood_vault": prior.robinhood_vault,
        "robinhood_signer": prior.robinhood_signer,
        "robinhood_nonce_aligned": true,
        "unknown_robinhood_position": false,
        "nav_micros": 100_000_000,
        "daily_turnover_micros": 0,
        "active_episodes": 0,
        "flat": true,
        "spot_decimals": 6,
        "spot_config_version": 1,
        "ui_multiplier_e18": "500000000000000000",
        "next_client_order_index": 1,
        "next_unwind_order_index": 2,
        "observed_at_ms": 1,
    }))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
        UPDATE live_scheduler_work
        SET state = 'running',
            lease_owner = 'pre-release-worker',
            lease_until = now() + interval '1 hour'
        WHERE evaluation_id = $1
        "#,
    )
    .bind(scheduler_evaluation_id)
    .execute(&pool)
    .await
    .unwrap();

    let mut prior_intent = intent();
    prior_intent.execution_account_id = prior.execution_account_id.clone();
    prior_intent.agent_id = prior.agent_id.clone();
    prior_intent.strategy_manifest_sha256 = prior.strategy_manifest_sha256.clone();
    prior_intent.lighter_account_index = u64::try_from(prior.lighter_account_index).unwrap();
    prior_intent.lighter_api_key_index = u8::try_from(prior.lighter_api_key_index).unwrap();
    prior_intent.robinhood_vault = prior.robinhood_vault.clone();
    prior_intent.robinhood_signer = prior.robinhood_signer.clone();
    prior_intent.derive_identifiers().unwrap();
    prior_intent.validate_for_unwind().unwrap();
    let prior_payload_sha256 = calculate_intent_payload_sha256(&prior_intent).unwrap();
    let mut prior_saga = ExecutionSaga::new(&prior_intent.id).unwrap();
    for event in [
        ExecutionEvent::PrecheckPassed,
        ExecutionEvent::PerpSubmitted,
        ExecutionEvent::PerpFilled {
            filled_base: prior_intent.perp_base_amount,
        },
        ExecutionEvent::SpotSubmitted,
        ExecutionEvent::SpotConfirmed {
            received_raw: prior_intent.raw_spot_amount,
        },
    ] {
        prior_saga.apply(event).unwrap();
    }
    let mut prior_status_saga = ExecutionSaga::new(&prior_intent.id).unwrap();
    prior_status_saga
        .apply(ExecutionEvent::PrecheckPassed)
        .unwrap();
    sqlx::query(
        r#"
        INSERT INTO execution_intents (
            id, execution_account_id, agent_id, source_evaluation_id, risk_version,
            strategy_version, symbol, direction, payload, payload_sha256,
            saga, saga_version, active
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, 'long_spot_short_perp',
            $8, $9, $10, $11, TRUE
        )
        "#,
    )
    .bind(&prior_intent.id)
    .bind(&prior_intent.execution_account_id)
    .bind(&prior_intent.agent_id)
    .bind(&prior_intent.source_evaluation_id)
    .bind(&prior_intent.risk_version)
    .bind(&prior_intent.evidence.strategy_version)
    .bind(&prior_intent.symbol)
    .bind(sqlx::types::Json(&prior_intent))
    .bind(&prior_payload_sha256)
    .bind(sqlx::types::Json(&prior_status_saga))
    .bind(i64::try_from(prior_status_saga.version).unwrap())
    .execute(&pool)
    .await
    .unwrap();

    let current = registration(
        "current-basis-account",
        "current-basis-agent",
        82,
        "0x0000000000000000000000000000000000000091",
        "0x0000000000000000000000000000000000000092",
        "0x0000000000000000000000000000000000000093",
    );
    let store = Store::from_pool(pool.clone());
    assert!(matches!(
        store.register_execution_account(&current).await,
        Err(StoreError::AccountCapacityExceeded)
    ));

    for (index, (command_id, command, status)) in [
        ("pre-release-launch", "launch", "processing"),
        ("pre-release-pause", "pause", "reducing"),
        ("pre-release-resume", "resume", "processing"),
        ("pre-release-close", "close", "reducing"),
        (
            "pre-release-withdraw",
            "withdraw",
            "awaiting_owner_signature",
        ),
    ]
    .into_iter()
    .enumerate()
    {
        sqlx::query(
            r#"
            INSERT INTO execution_account_commands
                (command_id, execution_account_id, agent_id, command, request_sha256, status)
            VALUES ($1, $2, $3, $4, repeat($5, 64), $6)
            "#,
        )
        .bind(command_id)
        .bind(&prior.execution_account_id)
        .bind(&prior.agent_id)
        .bind(command)
        .bind(char::from(b'a' + u8::try_from(index).unwrap()).to_string())
        .bind(status)
        .execute(&pool)
        .await
        .unwrap();
    }

    sqlx::raw_sql(include_str!(
        "../migrations/0018_repin_private_strategy_policy.sql"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!(
        "../migrations/0019_robinhood_snapshot_source_blocks.sql"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!(
        "../migrations/0020_release_blocked_exit_quotes.sql"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!(
        "../migrations/0021_require_runtime_readiness.sql"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!(
        "../../runtime/live-scheduler/migrations/0004_repin_private_strategy_policy.sql"
    ))
    .execute(&pool)
    .await
    .unwrap();

    let prior_state = sqlx::query_as::<_, (String, String, String, String)>(
        r#"
        SELECT account.status, control.mode, account.strategy_manifest_sha256,
               registration.strategy_manifest_sha256
        FROM execution_accounts account
        JOIN execution_account_control control USING (execution_account_id)
        JOIN execution_account_registrations registration USING (execution_account_id)
        WHERE account.execution_account_id = $1
        "#,
    )
    .bind(&prior.execution_account_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        prior_state,
        (
            "blocked".into(),
            "HALTED".into(),
            PRIOR_BASIS_AAPL_V1_MANIFEST_SHA256.into(),
            PRIOR_BASIS_AAPL_V1_MANIFEST_SHA256.into(),
        )
    );
    let migrated_commands = sqlx::query_as::<_, (String, String, String, String)>(
        r#"
        SELECT command, status, result->>'reason', result->>'control_mode'
        FROM execution_account_commands
        WHERE command_id LIKE 'pre-release-%'
        ORDER BY command
        "#,
    )
    .fetch_all(&pool)
    .await
    .unwrap();
    assert_eq!(migrated_commands.len(), 5);
    assert!(migrated_commands.iter().all(|row| {
        row.1 == "blocked"
            && row.2 == "strategy release changed; reconcile and reprovision"
            && row.3 == "HALTED"
    }));
    assert!(migrated_commands
        .iter()
        .any(|row| row.0 == "close" && row.1 == "blocked"));
    let old_close_completion = sqlx::query(
        r#"
        UPDATE execution_account_commands
        SET status = 'completed',
            result = jsonb_build_object(
                'control_mode', 'HALTED',
                'reconciled_flat', true,
                'owner_actions', jsonb_build_array()
            )
        WHERE command_id = 'pre-release-close'
        "#,
    )
    .execute(&pool)
    .await
    .unwrap_err();
    assert_eq!(
        old_close_completion
            .as_database_error()
            .and_then(|error| error.code()),
        Some(std::borrow::Cow::Borrowed("23514"))
    );
    let migration_events = sqlx::query_scalar::<_, i64>(
        r#"
        SELECT count(*)
        FROM execution_account_command_events
        WHERE command_id LIKE 'pre-release-%'
          AND status = 'blocked'
          AND details->>'reason' =
              'strategy release changed; reconcile and reprovision'
        "#,
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(migration_events, 5);

    let scheduler_work = sqlx::query_as::<_, (String, String, Option<String>, Option<String>)>(
        r#"
        SELECT state, last_error, lease_owner, lease_until::text
        FROM live_scheduler_work
        WHERE evaluation_id = $1
        "#,
    )
    .bind(scheduler_evaluation_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        scheduler_work,
        (
            "blocked".into(),
            "strategy release changed; resubmit under current strategy release".into(),
            None,
            None,
        )
    );
    let scheduler_events = sqlx::query_scalar::<_, i64>(
        r#"
        SELECT count(*)
        FROM live_scheduler_events
        WHERE evaluation_id = $1
          AND kind = 'blocked'
          AND details->>'reason' =
              'strategy release changed; resubmit under current strategy release'
          AND details_sha256 =
              '3e8fdc2161decbb8db3d498a087b2b87040b9929b9447098535a6ae124b019f2'
        "#,
    )
    .bind(scheduler_evaluation_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(scheduler_events, 1);

    sqlx::query(
        r#"
        INSERT INTO execution_account_commands
            (command_id, execution_account_id, agent_id, command, request_sha256, status)
        VALUES ($1, $2, $3, 'pause', repeat('e', 64), 'processing')
        "#,
    )
    .bind("post-release-pause-probe")
    .bind(&prior.execution_account_id)
    .bind(&prior.agent_id)
    .execute(&pool)
    .await
    .unwrap();
    assert!(matches!(
        store
            .account_command_status(
                &AccountCommandStatusRequest {
                    command_id: "post-release-pause-probe".into(),
                    execution_account_id: prior.execution_account_id.clone(),
                },
                1,
            )
            .await,
        Err(StoreError::AccountCommandBlocked)
    ));
    let release_control = sqlx::query_as::<_, (String, String)>(
        r#"
        SELECT control.mode, control.reason
        FROM execution_account_control control
        WHERE control.execution_account_id = $1
        "#,
    )
    .bind(&prior.execution_account_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        release_control,
        (
            "HALTED".into(),
            "strategy release changed; reconcile and reprovision".into(),
        )
    );
    let probe_status = sqlx::query_scalar::<_, String>(
        "SELECT status FROM execution_account_commands WHERE command_id = $1",
    )
    .bind("post-release-pause-probe")
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(probe_status, "processing");
    sqlx::query("UPDATE execution_account_commands SET status = 'blocked' WHERE command_id = $1")
        .bind("post-release-pause-probe")
        .execute(&pool)
        .await
        .unwrap();

    let predecessor_status = store
        .account_execution_status(&prior.execution_account_id)
        .await
        .unwrap();
    assert!(predecessor_status.active);
    assert!(!predecessor_status.flat);
    assert_eq!(
        predecessor_status.strategy_manifest_sha256,
        PRIOR_BASIS_AAPL_V1_MANIFEST_SHA256
    );
    sqlx::query(
        "UPDATE execution_intents SET saga = $2, saga_version = $3, updated_at = now() \
         WHERE id = $1",
    )
    .bind(&prior_intent.id)
    .bind(sqlx::types::Json(&prior_saga))
    .bind(i64::try_from(prior_saga.version).unwrap())
    .execute(&pool)
    .await
    .unwrap();

    let rejected_pause = AccountCommandRequest {
        command_id: "prior-basis-pause".into(),
        execution_account_id: prior.execution_account_id.clone(),
        agent_id: prior.agent_id.clone(),
        command: "pause".into(),
        requested_at_ms: 1,
    };
    assert!(matches!(
        store.submit_account_command(&rejected_pause, 1).await,
        Err(StoreError::AccountCommandBlocked)
    ));
    sqlx::query(
        "UPDATE execution_account_control SET reason = 'wrong release reason' \
         WHERE execution_account_id = $1",
    )
    .bind(&prior.execution_account_id)
    .execute(&pool)
    .await
    .unwrap();
    let wrong_reason_close = AccountCommandRequest {
        command_id: "prior-wrong-reason-close".into(),
        execution_account_id: prior.execution_account_id.clone(),
        agent_id: prior.agent_id.clone(),
        command: "close".into(),
        requested_at_ms: 1,
    };
    assert!(matches!(
        store.submit_account_command(&wrong_reason_close, 1).await,
        Err(StoreError::AccountCommandBlocked)
    ));
    sqlx::query(
        "UPDATE execution_account_control \
         SET reason = 'strategy release changed; reconcile and reprovision' \
         WHERE execution_account_id = $1",
    )
    .bind(&prior.execution_account_id)
    .execute(&pool)
    .await
    .unwrap();
    let rejected_commands = sqlx::query_scalar::<_, i64>(
        "SELECT count(*) FROM execution_account_commands \
         WHERE command_id IN ('prior-basis-pause', 'prior-wrong-reason-close')",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(rejected_commands, 0);

    let mut legacy = registration(
        "legacy-basis-account",
        "legacy-basis-agent",
        83,
        "0x00000000000000000000000000000000000000a1",
        "0x00000000000000000000000000000000000000a2",
        "0x00000000000000000000000000000000000000a3",
    );
    legacy.strategy_manifest_sha256 = BASIS_AAPL_V1_LEGACY_MANIFEST_SHA256.into();
    legacy.binding_sha256 = legacy.calculate_binding_sha256();
    sqlx::query(
        r#"
        INSERT INTO execution_accounts (
            execution_account_id, agent_id, strategy_version, risk_version, status,
            lighter_account_index, lighter_api_key_index, robinhood_vault,
            robinhood_signer, owner_address, strategy_manifest_sha256, binding_sha256
        ) VALUES ($1, $2, $3, $4, 'blocked', $5, $6, $7, $8, $9, $10, $11)
        "#,
    )
    .bind(&legacy.execution_account_id)
    .bind(&legacy.agent_id)
    .bind(&legacy.strategy_version)
    .bind(&legacy.risk_version)
    .bind(legacy.lighter_account_index)
    .bind(legacy.lighter_api_key_index)
    .bind(&legacy.robinhood_vault)
    .bind(&legacy.robinhood_signer)
    .bind(&legacy.robinhood_owner)
    .bind(&legacy.strategy_manifest_sha256)
    .bind(&legacy.binding_sha256)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO execution_account_control (execution_account_id, mode, reason) \
         VALUES ($1, 'HALTED', 'strategy release changed; reconcile and reprovision')",
    )
    .bind(&legacy.execution_account_id)
    .execute(&pool)
    .await
    .unwrap();
    let missing_registration_close = AccountCommandRequest {
        command_id: "legacy-missing-registration-close".into(),
        execution_account_id: legacy.execution_account_id.clone(),
        agent_id: legacy.agent_id.clone(),
        command: "close".into(),
        requested_at_ms: 1,
    };
    assert!(matches!(
        store
            .submit_account_command(&missing_registration_close, 1)
            .await,
        Err(StoreError::AccountCommandBlocked)
    ));
    sqlx::query(
        r#"
        INSERT INTO execution_account_registrations (
            execution_account_id, agent_id, strategy_version, risk_version,
            strategy_manifest_sha256, lighter_account_index, lighter_api_key_index,
            robinhood_owner, robinhood_vault, robinhood_signer, binding_sha256
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
        "#,
    )
    .bind(&legacy.execution_account_id)
    .bind(&legacy.agent_id)
    .bind(&legacy.strategy_version)
    .bind(&legacy.risk_version)
    .bind(&legacy.strategy_manifest_sha256)
    .bind(legacy.lighter_account_index)
    .bind(legacy.lighter_api_key_index)
    .bind(&legacy.robinhood_owner)
    .bind(&legacy.robinhood_vault)
    .bind(&legacy.robinhood_signer)
    .bind(&legacy.binding_sha256)
    .execute(&pool)
    .await
    .unwrap();
    let legacy_status = store
        .account_execution_status(&legacy.execution_account_id)
        .await
        .unwrap();
    assert!(!legacy_status.active);
    assert!(legacy_status.flat);
    assert_eq!(
        legacy_status.strategy_manifest_sha256,
        BASIS_AAPL_V1_LEGACY_MANIFEST_SHA256
    );
    let legacy_close = store
        .submit_account_command(
            &AccountCommandRequest {
                command_id: "legacy-basis-close".into(),
                execution_account_id: legacy.execution_account_id.clone(),
                agent_id: legacy.agent_id.clone(),
                command: "close".into(),
                requested_at_ms: 1,
            },
            1,
        )
        .await
        .unwrap();
    assert_eq!(legacy_close.status, "reducing");

    let registered = store.register_execution_account(&current).await.unwrap();
    assert!(registered.created);
    assert_eq!(registered.response.account_status, "active");
    assert_eq!(registered.response.control_mode, "REDUCE_ONLY");

    let now_ms = sqlx::query_scalar::<_, i64>(
        "SELECT floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    let mut snapshots = account_snapshots_for(
        &current.execution_account_id,
        u64::try_from(current.lighter_account_index).unwrap(),
        &current.robinhood_vault,
        &current.robinhood_signer,
        &current.robinhood_owner,
    );
    for snapshot in &mut snapshots {
        let observed_at_ms = now_ms / 1_000 * 1_000;
        set_snapshot_times(snapshot, observed_at_ms, now_ms, observed_at_ms + 5_000);
        store.record_account_snapshot(snapshot).await.unwrap();
    }
    sqlx::query(
        r#"
        UPDATE execution_account_readiness
        SET venue_approved = TRUE, oracle_healthy = TRUE, sequencer_healthy = TRUE,
            reconciliation_ready = TRUE, exit_authority_ready = TRUE, updated_at = now()
        WHERE execution_account_id = $1
        "#,
    )
    .bind(&current.execution_account_id)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE execution_control \
         SET mode = 'ACTIVE', reason = 'integration test runtime readiness' \
         WHERE singleton",
    )
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE execution_strategy_control \
         SET mode = 'ACTIVE', reason = 'integration test runtime readiness' \
         WHERE strategy_version = $1",
    )
    .bind(&current.strategy_version)
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(
        "UPDATE execution_rollout_readiness \
         SET alerting_ready = TRUE, safe_rotation_ready = TRUE, \
             version = version + 1, updated_at = now() \
         WHERE singleton",
    )
    .execute(&pool)
    .await
    .unwrap();
    let launch_ms = sqlx::query_scalar::<_, i64>(
        "SELECT floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    let launched = store
        .submit_account_command(
            &AccountCommandRequest {
                command_id: "current-basis-launch".into(),
                execution_account_id: current.execution_account_id,
                agent_id: current.agent_id,
                command: "launch".into(),
                requested_at_ms: u64::try_from(launch_ms).unwrap(),
            },
            u64::try_from(launch_ms).unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(launched.status, "completed");
    assert_eq!(launched.control_mode, "ACTIVE");

    let recovery_now = sqlx::query_scalar::<_, i64>(
        "SELECT floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    let recovery_now = u64::try_from(recovery_now).unwrap();
    let observed_at = i64::try_from(recovery_now / 1_000 * 1_000).unwrap();
    let mut recovery_snapshots = account_snapshots_for(
        &prior.execution_account_id,
        u64::try_from(prior.lighter_account_index).unwrap(),
        &prior.robinhood_vault,
        &prior.robinhood_signer,
        &prior.robinhood_owner,
    );
    for snapshot in &mut recovery_snapshots {
        snapshot.payload["flat"] = serde_json::json!(false);
        set_snapshot_times(
            snapshot,
            observed_at,
            i64::try_from(recovery_now).unwrap(),
            observed_at + 5_000,
        );
        store.record_account_snapshot(snapshot).await.unwrap();
    }

    let close = store
        .submit_account_command(
            &AccountCommandRequest {
                command_id: "prior-basis-close".into(),
                execution_account_id: prior.execution_account_id.clone(),
                agent_id: prior.agent_id.clone(),
                command: "close".into(),
                requested_at_ms: recovery_now,
            },
            recovery_now,
        )
        .await
        .unwrap();
    assert_eq!(close.status, "reducing");
    assert_eq!(close.control_mode, "HALTED");

    let observation_candidate = sqlx::query_as::<_, (bool, String)>(
        r#"
        SELECT release_recovery, target_strategy_manifest_sha256
        FROM execution_account_observation_candidates_v1
        WHERE execution_account_id = $1
        "#,
    )
    .bind(&prior.execution_account_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(
        observation_candidate,
        (true, PRIOR_BASIS_AAPL_V1_MANIFEST_SHA256.into())
    );
    let open = store
        .open_episode(&prior.execution_account_id, &prior_intent.id, recovery_now)
        .await
        .unwrap();
    assert_eq!(open.schema_version, 2);
    assert_eq!(
        open.target_strategy_manifest_sha256,
        PRIOR_BASIS_AAPL_V1_MANIFEST_SHA256
    );
    assert_eq!(open.phase, "perp_and_spot");

    sqlx::query(
        r#"
        INSERT INTO execution_market_configs
            (manifest_id, symbol, spot_token, lighter_market_index, spot_decimals,
             perp_base_decimals, perp_price_decimals, spot_config_version,
             ui_multiplier_e18, max_price_deviation_bps, max_spot_slippage_bps,
             max_unwind_price_deviation_bps, review_record_sha256, valid_from, valid_until)
        VALUES ($1, 'AAPL', $2, 101, 6, 6, 3, 1, $3, 100, 500, 2500, $4,
                TIMESTAMPTZ 'epoch',
                TIMESTAMPTZ 'epoch' + $5 * interval '1 millisecond')
        "#,
    )
    .bind(&prior_intent.evidence.market_manifest)
    .bind(&prior_intent.spot_token)
    .bind(prior_intent.expected_ui_multiplier.to_string())
    .bind("abababababababababababababababababababababababababababababababab")
    .bind(i64::try_from(recovery_now + 86_400_000).unwrap())
    .execute(&pool)
    .await
    .unwrap();

    let quote = NewMarketQuote {
        source: "execution-authority".into(),
        source_session: "prior-close-session".into(),
        source_event_id: "prior-close-quote".into(),
        source_sequence: 1,
        execution_account_id: Some(prior.execution_account_id.clone()),
        market_manifest: prior_intent.evidence.market_manifest.clone(),
        strategy_manifest_sha256: Some(BASIS_AAPL_V1_MANIFEST_SHA256.into()),
        target_strategy_manifest_sha256: Some(prior.strategy_manifest_sha256.clone()),
        route_sha256: Some(BASIS_AAPL_V1_ROUTE_SHA256.into()),
        lighter_market_index: Some(prior_intent.lighter_market_index),
        quote_block_hash: prior_intent.evidence.quote_block_hash.clone(),
        mark_price: 25_000,
        expected_ui_multiplier: prior_intent.expected_ui_multiplier.to_string(),
        min_oracle_round_id: prior_intent.min_oracle_round_id.to_string(),
        publisher_at_ms: i64::try_from(recovery_now).unwrap(),
        received_at_ms: i64::try_from(recovery_now).unwrap(),
        expires_at_ms: i64::try_from(recovery_now + 60_000).unwrap(),
        intent_id: Some(prior_intent.id.clone()),
        spot_unwind_amount_in: Some(prior_saga.spot_received_raw.to_string()),
        spot_unwind_expected_amount_out: Some("25000000".into()),
        unwind_phase: Some("perp_and_spot".into()),
        perp_unwind_base_amount: Some(prior_saga.perp_filled_base),
        perp_unwind_limit_price: Some(30_000),
        submission_deadline_ms: Some(i64::try_from(recovery_now + 60_000).unwrap()),
        reconciliation_deadline_ms: Some(i64::try_from(recovery_now + 120_000).unwrap()),
    };
    store.record_market_quote(&quote).await.unwrap();

    let perp_action = store
        .claim_action("prior-close-worker", Duration::from_secs(30))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(perp_action.kind, ActionKind::UnwindPerp);
    assert_eq!(
        perp_action.intent.strategy_manifest_sha256,
        PRIOR_BASIS_AAPL_V1_MANIFEST_SHA256
    );
    assert!(store
        .bind_exit_authority(
            &perp_action.id,
            "prior-close-worker",
            &perp_action.lease_token,
            recovery_now,
        )
        .await
        .unwrap());
    let bound_payload = sqlx::query_scalar::<_, serde_json::Value>(
        "SELECT payload FROM execution_actions WHERE id = $1",
    )
    .bind(&perp_action.id)
    .fetch_one(&pool)
    .await
    .unwrap();
    store
        .complete_action(
            &perp_action.id,
            "prior-close-worker",
            &perp_action.lease_token,
            Some(ExecutionEvent::PerpUnwindCompleted {
                unwound_base: prior_saga.perp_filled_base,
            }),
            serde_json::json!({"filled_base": prior_saga.perp_filled_base}),
            Some(NextAction {
                kind: ActionKind::UnwindSpot,
                key: "prior-release-unwind-spot".into(),
                payload: serde_json::json!({
                    "spot_amount": prior_saga.spot_received_raw.to_string(),
                    "exit_authority": bound_payload.get("exit_authority"),
                    "exit_reason": "operator_exit",
                    "control_command_id": "prior-basis-close",
                    "authority_wait_deadline_ms": recovery_now + 900_000,
                }),
            }),
        )
        .await
        .unwrap();
    let spot_action = store
        .claim_action("prior-close-worker", Duration::from_secs(30))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(spot_action.kind, ActionKind::UnwindSpot);
    assert_eq!(
        spot_action.intent.strategy_manifest_sha256,
        PRIOR_BASIS_AAPL_V1_MANIFEST_SHA256
    );
    store
        .complete_action(
            &spot_action.id,
            "prior-close-worker",
            &spot_action.lease_token,
            Some(ExecutionEvent::Closed),
            serde_json::json!({"amount_in": prior_saga.spot_received_raw.to_string()}),
            None,
        )
        .await
        .unwrap();

    let completion_now = recovery_now + 1_000;
    for snapshot in &mut recovery_snapshots {
        snapshot.source_sequence = 2;
        snapshot.payload["flat"] = serde_json::json!(true);
        set_snapshot_times(
            snapshot,
            i64::try_from(completion_now / 1_000 * 1_000).unwrap(),
            i64::try_from(completion_now).unwrap(),
            i64::try_from(completion_now / 1_000 * 1_000 + 5_000).unwrap(),
        );
        store.record_account_snapshot(snapshot).await.unwrap();
    }
    let awaiting_revocation = store
        .account_command_status(
            &AccountCommandStatusRequest {
                command_id: "prior-basis-close".into(),
                execution_account_id: prior.execution_account_id.clone(),
            },
            completion_now,
        )
        .await
        .unwrap();
    assert_eq!(awaiting_revocation.status, "awaiting_owner_signature");
    assert!(awaiting_revocation.reconciled_flat);
    assert!(!awaiting_revocation.agent_revoked);
    assert_eq!(awaiting_revocation.owner_actions.len(), 1);
    assert_eq!(awaiting_revocation.owner_actions[0].data, "0x51755334");

    let revoked_now = completion_now + 1_000;
    for snapshot in &mut recovery_snapshots {
        snapshot.source_sequence = 3;
        if snapshot.source == "robinhood-chain" {
            snapshot.payload["agent_enabled"] = serde_json::json!(false);
            snapshot.payload["risk_mode"] = serde_json::json!("HALTED");
        }
        set_snapshot_times(
            snapshot,
            i64::try_from(revoked_now / 1_000 * 1_000).unwrap(),
            i64::try_from(revoked_now).unwrap(),
            i64::try_from(revoked_now / 1_000 * 1_000 + 5_000).unwrap(),
        );
        store.record_account_snapshot(snapshot).await.unwrap();
    }
    let latest_only = store
        .account_command_status(
            &AccountCommandStatusRequest {
                command_id: "prior-basis-close".into(),
                execution_account_id: prior.execution_account_id.clone(),
            },
            revoked_now,
        )
        .await
        .unwrap();
    assert_eq!(latest_only.status, "awaiting_owner_signature");
    assert!(!latest_only.agent_revoked);

    let finalized_now = revoked_now + 1_000;
    for snapshot in &mut recovery_snapshots {
        snapshot.source_sequence = 4;
        if snapshot.source == "robinhood-chain" {
            snapshot.payload["finalized_agent_address"] =
                serde_json::json!("0x0000000000000000000000000000000000000000");
            snapshot.payload["finalized_agent_enabled"] = serde_json::json!(false);
            snapshot.payload["finalized_agent_revoked"] = serde_json::json!(true);
            snapshot.payload["finalized_risk_mode"] = serde_json::json!("HALTED");
        }
        set_snapshot_times(
            snapshot,
            i64::try_from(finalized_now / 1_000 * 1_000).unwrap(),
            i64::try_from(finalized_now).unwrap(),
            i64::try_from(finalized_now / 1_000 * 1_000 + 5_000).unwrap(),
        );
        store.record_account_snapshot(snapshot).await.unwrap();
    }
    let closed = store
        .account_command_status(
            &AccountCommandStatusRequest {
                command_id: "prior-basis-close".into(),
                execution_account_id: prior.execution_account_id.clone(),
            },
            finalized_now,
        )
        .await
        .unwrap();
    assert_eq!(closed.status, "completed");
    assert!(closed.reconciled_flat);
    assert!(closed.agent_revoked);
    let prior_account_status = sqlx::query_scalar::<_, String>(
        "SELECT status FROM execution_accounts WHERE execution_account_id = $1",
    )
    .bind(&prior.execution_account_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(prior_account_status, "closed");

    let mut replacement = registration(
        "replacement-basis-account",
        "replacement-basis-agent",
        84,
        &prior.robinhood_owner,
        "0x00000000000000000000000000000000000000b2",
        "0x00000000000000000000000000000000000000b3",
    );
    replacement.binding_sha256 = replacement.calculate_binding_sha256();
    assert!(matches!(
        store.register_execution_account(&replacement).await,
        Err(StoreError::AccountOwnerRotationRequired)
    ));
}

async fn insert_transition(
    pool: &PgPool,
    strategy: &str,
    from: &str,
    to: &str,
    digest: &str,
) -> Result<(), sqlx::Error> {
    sqlx::query(
        "INSERT INTO execution_promotion_events \
         (strategy_version, from_state, to_state, evidence_sha256, approved_by) \
         VALUES ($1, $2, $3, $4, $5)",
    )
    .bind(strategy)
    .bind(from)
    .bind(to)
    .bind(digest)
    .bind("approval-record")
    .execute(pool)
    .await?;
    Ok(())
}

fn approved_evidence() -> PromotionEvidence {
    PromotionEvidence::Research {
        hypothesis_registered: true,
        testing_family_registered: true,
        frozen_dataset_verified: true,
        walk_forward_verified: true,
        adjusted_p_value_ppb: 1_350_000,
        deflated_sharpe_probability_ppm: 990_000,
        bootstrap_net_return_lower_bound_ppm: 1,
        canary_capacity_micros: 25_000_000,
        capacity_curve_bounded: true,
        internal_audit_approved: true,
        executor_review_approved: true,
        key_review_approved: true,
        restore_drill_approved: true,
    }
}

fn intent() -> PairIntent {
    let mut intent = PairIntent {
        version: PAIR_INTENT_VERSION,
        id: String::new(),
        spot_unwind_intent_id: String::new(),
        execution_account_id: "singleton-mainnet-canary".into(),
        agent_id: "singleton-mainnet-canary".into(),
        source_evaluation_id: "0x3333333333333333333333333333333333333333333333333333333333333333"
            .into(),
        risk_version: CANARY_RISK_VERSION.into(),
        strategy_manifest_sha256: execution::BASIS_AAPL_V1_MANIFEST_SHA256.into(),
        lighter_account_index: 7,
        lighter_api_key_index: 4,
        robinhood_vault: "0x0000000000000000000000000000000000000002".into(),
        robinhood_signer: "0x0000000000000000000000000000000000000003".into(),
        symbol: "AAPL".into(),
        spot_token: "0xaf3d76f1834a1d425780943c99ea8a608f8a93f9".into(),
        lighter_market_index: 101,
        spot_side: SpotSide::Buy,
        perp_side: PerpSide::Short,
        spot_notional_micros: 25_000_000,
        perp_notional_micros: 25_000_000,
        nav_micros: 10_000_000_000,
        raw_spot_amount: 2_000_000,
        settlement_amount_in: 25_000_000,
        minimum_spot_amount_out: 1_990_000,
        minimum_unwind_settlement_out: 24_000_000,
        expected_ui_multiplier: 500_000_000_000_000_000,
        min_oracle_round_id: 1,
        spot_decimals: 6,
        spot_config_version: 1,
        perp_base_amount: 1_000_000,
        perp_base_decimals: 6,
        perp_price_decimals: 3,
        perp_limit_price: 25_000,
        client_order_index: 1,
        perp_unwind_price: 30_000,
        unwind_client_order_index: 2,
        max_unwind_attempts: 3,
        perp_order_expiry_ms: 301_000,
        emergency_deadline_ms: 601_000,
        reconciliation_deadline_ms: 86_401_000,
        leverage_micros: 1_000_000,
        created_at_ms: 1_000,
        deadline_ms: 1_500,
        evidence: FrozenEvidence {
            dataset_manifest: "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
                .into(),
            strategy_version: CANARY_RISK_VERSION.into(),
            market_manifest: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
                .into(),
            quote_block_hash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
                .into(),
            quote_received_at_ms: 900,
            quote_expires_at_ms: 1_500,
            ui_multiplier_e18: 500_000_000_000_000_000,
            perp_mark_price: 25_000,
            estimated_total_cost_micros: 10_000,
        },
    };
    intent.derive_identifiers().unwrap();
    intent
}

fn account_snapshots() -> [NewAccountSnapshot; 2] {
    account_snapshots_for(
        "singleton-mainnet-canary",
        7,
        "0x0000000000000000000000000000000000000002",
        "0x0000000000000000000000000000000000000003",
        "0x0000000000000000000000000000000000000004",
    )
}

fn account_snapshots_for(
    execution_account_id: &str,
    account_index: u64,
    vault: &str,
    signer: &str,
    owner: &str,
) -> [NewAccountSnapshot; 2] {
    [
        NewAccountSnapshot {
            execution_account_id: execution_account_id.into(),
            source: "lighter-auth".into(),
            source_session: format!("lighter-{execution_account_id}"),
            source_sequence: 1,
            payload: serde_json::json!({
                "account_index": account_index,
                "api_key_index": 4,
                "market_index": 101,
                "nonce_aligned": true,
                "no_unknown_orders": true,
                "no_unknown_positions": true,
                "collateral_ready": true,
                "maintenance_margin_ratio_micros": 2_000_000,
                "collateral_micros": 50_000_000,
                "maintenance_margin_micros": 25_000_000,
                "flat": true,
            }),
            observed_at_ms: 1_198,
            received_at_ms: 1_199,
            expires_at_ms: 1_500,
        },
        NewAccountSnapshot {
            execution_account_id: execution_account_id.into(),
            source: "robinhood-chain".into(),
            source_session: format!("robinhood-{execution_account_id}"),
            source_sequence: 1,
            payload: serde_json::json!({
                "vault_address": vault,
                "signer_address": signer,
                "funding_ready": true,
                "wiring_verified": true,
                "finality_healthy": true,
                "flat": true,
                "owner_address": owner,
                "agent_enabled": true,
                "risk_mode": "ACTIVE",
                "finalized_agent_address": signer,
                "finalized_agent_enabled": true,
                "finalized_agent_revoked": false,
                "global_mode": "ACTIVE",
                "finalized_global_mode": "ACTIVE",
                "finalized_risk_mode": "ACTIVE",
                "settlement_balance_raw": "25000000",
                "nonce_aligned": true,
                "spot_config_version": 1,
                "stock_decimals": 6,
                "ui_multiplier_e18": "500000000000000000",
                "new_ui_multiplier_e18": "500000000000000000",
                "oracle_paused": false,
                "oracle_healthy": true,
                "sequencer_healthy": true,
                "signer_gas_ready": true,
                "finalized_number": 100,
                "finalized_hash": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                "finalized_timestamp": 1,
                "source_block_number": 110,
                "source_block_hash": "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
                "source_block_timestamp": 1,
            }),
            observed_at_ms: 1_000,
            received_at_ms: 1_199,
            expires_at_ms: 1_500,
        },
    ]
}

fn set_snapshot_times(
    snapshot: &mut NewAccountSnapshot,
    observed_at_ms: i64,
    received_at_ms: i64,
    expires_at_ms: i64,
) {
    assert_eq!(observed_at_ms % 1_000, 0);
    snapshot.observed_at_ms = observed_at_ms;
    snapshot.received_at_ms = received_at_ms;
    snapshot.expires_at_ms = expires_at_ms;
    if snapshot.source == "robinhood-chain" {
        let source_timestamp = observed_at_ms / 1_000;
        snapshot.payload["source_block_timestamp"] = serde_json::json!(source_timestamp);
        snapshot.payload["finalized_timestamp"] =
            serde_json::json!(source_timestamp.saturating_sub(900).max(1));
    }
}

async fn register_account(
    pool: &PgPool,
    execution_account_id: &str,
    agent_id: &str,
    lighter_account_index: i64,
    vault: &str,
    signer: &str,
    owner: &str,
) {
    let canonical = registration(
        execution_account_id,
        agent_id,
        lighter_account_index,
        owner,
        vault,
        signer,
    );
    sqlx::query(
        r#"
        INSERT INTO execution_accounts
            (execution_account_id, agent_id, strategy_version, risk_version, status,
             lighter_account_index, lighter_api_key_index, robinhood_vault,
             robinhood_signer, owner_address, strategy_manifest_sha256, binding_sha256)
		VALUES ($1, $2, $3, $3, 'active', $4, 4, $5, $6, $7, $8, $9)
        "#,
    )
    .bind(execution_account_id)
    .bind(agent_id)
    .bind(CANARY_RISK_VERSION)
    .bind(lighter_account_index)
    .bind(vault)
    .bind(signer)
    .bind(owner)
    .bind(execution::BASIS_AAPL_V1_MANIFEST_SHA256)
    .bind(&canonical.binding_sha256)
    .execute(pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
		INSERT INTO execution_account_registrations
		    (execution_account_id, agent_id, strategy_version, risk_version,
		     strategy_manifest_sha256, lighter_account_index, lighter_api_key_index,
		     robinhood_owner, robinhood_vault, robinhood_signer, binding_sha256)
		VALUES ($1, $2, $3, $3, $4, $5, 4, $6, $7, $8, $9)
		"#,
    )
    .bind(execution_account_id)
    .bind(agent_id)
    .bind(CANARY_RISK_VERSION)
    .bind(execution::BASIS_AAPL_V1_MANIFEST_SHA256)
    .bind(lighter_account_index)
    .bind(owner)
    .bind(vault)
    .bind(signer)
    .bind(&canonical.binding_sha256)
    .execute(pool)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO execution_account_control (execution_account_id, mode, reason) VALUES ($1, 'ACTIVE', 'integration test')",
    )
    .bind(execution_account_id)
    .execute(pool)
    .await
    .unwrap();
    sqlx::query(
        r#"
        INSERT INTO execution_account_readiness
            (execution_account_id, venue_approved, oracle_healthy, sequencer_healthy,
             reconciliation_ready, exit_authority_ready, alerting_ready, safe_rotation_ready)
        VALUES ($1, TRUE, TRUE, TRUE, TRUE, TRUE, TRUE, TRUE)
        "#,
    )
    .bind(execution_account_id)
    .execute(pool)
    .await
    .unwrap();
}

fn registration(
    execution_account_id: &str,
    agent_id: &str,
    lighter_account_index: i64,
    owner: &str,
    vault: &str,
    signer: &str,
) -> AccountRegistrationRequest {
    let mut request = AccountRegistrationRequest {
        execution_account_id: execution_account_id.into(),
        agent_id: agent_id.into(),
        strategy_version: CANARY_RISK_VERSION.into(),
        risk_version: CANARY_RISK_VERSION.into(),
        strategy_manifest_sha256: execution::BASIS_AAPL_V1_MANIFEST_SHA256.into(),
        lighter_account_index,
        lighter_api_key_index: 4,
        robinhood_owner: owner.into(),
        robinhood_vault: vault.into(),
        robinhood_signer: signer.into(),
        binding_sha256: String::new(),
    };
    request.binding_sha256 = request.calculate_binding_sha256();
    request
}
