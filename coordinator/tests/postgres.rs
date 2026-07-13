use coordinator::store::{
    ActionKind, ExitRequest, NewMarketQuote, NewVenueEvent, NextAction, ObservationOutcome,
    RecoveryRequest, Store, StoreError,
};
use execution::{
    ExecutionEvent, ExecutionSaga, ExecutionState, FrozenEvidence, PairIntent, PerpSide, SpotSide,
};
use research::PromotionEvidence;
use sqlx::PgPool;

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

    let evidence = approved_evidence();
    let digest = evidence.calculate_hash();
    sqlx::query(
        "INSERT INTO execution_promotion_evidence \
         (strategy_version, evidence, evidence_sha256, approved_by) VALUES ($1, $2, $3, $4)",
    )
    .bind("strategy-v1")
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
        INSERT INTO execution_market_configs
            (manifest_id, symbol, spot_token, lighter_market_index, spot_decimals,
             perp_base_decimals, perp_price_decimals, spot_config_version, ui_multiplier_e18,
             max_price_deviation_bps, max_spot_slippage_bps,
             max_unwind_price_deviation_bps, review_record_sha256, valid_from, valid_until)
        VALUES ($1, 'NVDA', $2, 101, 6, 6, 3, 1, $3, 100, 500, 2500, $4,
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
        market_manifest: intent().evidence.market_manifest,
        quote_block_hash: intent().evidence.quote_block_hash,
        mark_price: 25_000,
        publisher_at_ms: 899,
        received_at_ms: 900,
        expires_at_ms: 1_500,
        intent_id: None,
        spot_unwind_amount_in: None,
        spot_unwind_expected_amount_out: None,
    };
    assert!(store.record_market_quote(&market_quote).await.unwrap());
    assert!(!store.record_market_quote(&market_quote).await.unwrap());
    let mut duplicate_quote = market_quote.clone();
    duplicate_quote.source_event_id = "quote-duplicate".into();
    duplicate_quote.source_sequence = 2;
    assert!(!store.record_market_quote(&duplicate_quote).await.unwrap());
    let mut mismatched_market = intent();
    mismatched_market.symbol = "AMD".into();
    assert!(matches!(
        store.create_intent(&mismatched_market, 1_200).await,
        Err(StoreError::MarketEvidenceMismatch)
    ));
    assert!(matches!(
        store.create_intent(&intent(), 1_200).await,
        Err(StoreError::MissingEvidence)
    ));

    let skipped = insert_transition(&pool, "strategy-v1", "registered", "shadow", &digest).await;
    assert!(skipped.is_err());

    for (from, to) in [
        ("registered", "research"),
        ("research", "shadow_eligible"),
        ("shadow_eligible", "shadow"),
        ("shadow", "audit_ready"),
        ("audit_ready", "canary_eligible"),
    ] {
        insert_transition(&pool, "strategy-v1", from, to, &digest)
            .await
            .unwrap();
    }

    let saga = store.create_intent(&intent(), 1_200).await.unwrap();
    assert_eq!(saga.state, ExecutionState::Prechecked);
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
            .assign_lighter_nonce(&action.id, "worker-1", &action.lease_token, 7, 2, 11)
            .await,
        Err(StoreError::LeaseLost)
    ));
    let action = reclaimed;
    let nonce = store
        .assign_lighter_nonce(&action.id, "worker-1", &action.lease_token, 7, 2, 11)
        .await
        .unwrap();
    assert_eq!(nonce, 11);
    assert_eq!(
        store
            .assign_lighter_nonce(&action.id, "worker-1", &action.lease_token, 7, 2, 99)
            .await
            .unwrap(),
        11
    );
    store
        .validate_lighter_nonce_binding(&action.id, 7, 2)
        .await
        .unwrap();
    store
        .authorize_entry_send(
            &action.id,
            "worker-1",
            &action.lease_token,
            action.control_version,
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
            )
            .await,
        Err(StoreError::CoordinatorHalted)
    ));
    assert!(matches!(
        store
            .assign_lighter_nonce(&action.id, "worker-1", &action.lease_token, 8, 2, 99)
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
    let mut second = intent();
    second.id = "0x3333333333333333333333333333333333333333333333333333333333333333".into();
    second.spot_unwind_intent_id =
        "0x4444444444444444444444444444444444444444444444444444444444444444".into();
    second.client_order_index = 10;
    second.unwind_client_order_index = 20;
    assert!(matches!(
        store.create_intent(&second, 1_200).await,
        Err(StoreError::Conflict)
    ));

    let mut retired = second.clone();
    retired.id = "0x5555555555555555555555555555555555555555555555555555555555555555".into();
    retired.spot_unwind_intent_id =
        "0x6666666666666666666666666666666666666666666666666666666666666666".into();
    retired.client_order_index = 30;
    retired.unwind_client_order_index = 40;
    let mut retirement = pool.begin().await.unwrap();
    sqlx::query("SELECT pg_advisory_xact_lock(hashtext($1))")
        .bind("strategy-v1")
        .execute(&mut *retirement)
        .await
        .unwrap();
    sqlx::query(
        "INSERT INTO execution_promotion_events \
         (strategy_version, from_state, to_state, evidence_sha256, approved_by) \
         VALUES ($1, $2, $3, $4, $5)",
    )
    .bind("strategy-v1")
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
        market_manifest: intent().evidence.market_manifest,
        quote_block_hash: "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
            .into(),
        mark_price: 25_000,
        publisher_at_ms: 1_099,
        received_at_ms: 1_100,
        expires_at_ms: 31_000,
        intent_id: Some(intent().id),
        spot_unwind_amount_in: Some("2000000".into()),
        spot_unwind_expected_amount_out: Some("25000000".into()),
    };
    assert!(store.record_market_quote(&exit_quote).await.unwrap());
    let exit_request = ExitRequest {
        intent_id: intent().id,
        quote_source_session: "exit-quote-session-1".into(),
        quote_source_event_id: "exit-quote-1".into(),
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
    assert_eq!(exiting.state, ExecutionState::Unwinding);
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
    assert!(store.record_market_quote(&recovery_quote).await.unwrap());
    let mut recovery_request = exit_request.clone();
    recovery_request.quote_source_event_id = "exit-quote-2".into();
    recovery_request.requested_at_ms = 1_400;
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
        market_manifest: intent().evidence.market_manifest,
        quote_block_hash: "0xcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
            .into(),
        mark_price: 25_000,
        publisher_at_ms: 1_499,
        received_at_ms: 1_500,
        expires_at_ms: 31_500,
        intent_id: Some(intent().id),
        spot_unwind_amount_in: Some("0".into()),
        spot_unwind_expected_amount_out: Some("0".into()),
    };
    assert!(store.record_market_quote(&zero_spot_quote).await.unwrap());
    let perp_only_exit = ExitRequest {
        intent_id: intent().id,
        quote_source_session: zero_spot_quote.source_session.clone(),
        quote_source_event_id: zero_spot_quote.source_event_id.clone(),
        perp_unwind_price: 30_000,
        minimum_unwind_settlement_out: "0".into(),
        requested_at_ms: 1_600,
        submission_deadline_ms: 31_000,
        reconciliation_deadline_ms: 91_000,
        reason: "operator_exit".into(),
    };
    let unwinding = store.request_exit(&perp_only_exit, 1_600).await.unwrap();
    assert_eq!(unwinding.state, ExecutionState::Unwinding);
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
    assert!(store.record_market_quote(&operator_quote).await.unwrap());
    let mut operator_exit = perp_only_exit.clone();
    operator_exit.quote_source_event_id = operator_quote.source_event_id.clone();
    operator_exit.requested_at_ms = 1_800;
    operator_exit.submission_deadline_ms = 31_600;
    operator_exit.reconciliation_deadline_ms = 91_600;
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
    PromotionEvidence {
        hypothesis_registered: true,
        testing_family_registered: true,
        frozen_dataset_verified: true,
        walk_forward_verified: true,
        adjusted_p_value_ppb: 1_350_000,
        deflated_sharpe_probability_ppm: 990_000,
        bootstrap_net_return_lower_bound_ppm: 1,
        canary_capacity_micros: 25_000_000,
        capacity_curve_bounded: true,
        capture_days: 180,
        continuous_shadow_days: 60,
        contract_audit_approved: true,
        executor_review_approved: true,
        key_review_approved: true,
        legal_approved: true,
        restore_drill_approved: true,
    }
}

fn intent() -> PairIntent {
    PairIntent {
        id: "0x1111111111111111111111111111111111111111111111111111111111111111".into(),
        spot_unwind_intent_id: "0x2222222222222222222222222222222222222222222222222222222222222222"
            .into(),
        symbol: "NVDA".into(),
        spot_token: "0x0000000000000000000000000000000000000001".into(),
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
            strategy_version: "strategy-v1".into(),
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
    }
}
