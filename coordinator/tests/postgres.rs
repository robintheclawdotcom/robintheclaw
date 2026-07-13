use coordinator::store::{Store, StoreError};
use execution::{FrozenEvidence, PairIntent, PerpSide, SpotSide};
use research::PromotionEvidence;
use sqlx::PgPool;

#[tokio::test]
#[ignore = "requires a disposable PostgreSQL database"]
async fn migration_and_promotion_gate_are_enforced() {
    let url = std::env::var("TEST_DATABASE_URL").expect("TEST_DATABASE_URL is required");
    let pool = PgPool::connect(&url).await.unwrap();
    sqlx::raw_sql(include_str!("../migrations/0001_execution.sql"))
        .execute(&pool)
        .await
        .unwrap();

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
        store.create_intent(&intent()).await,
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

    store.create_intent(&intent()).await.unwrap();
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
        id: "intent-1".into(),
        symbol: "NVDA".into(),
        spot_token: "0x0000000000000000000000000000000000000001".into(),
        lighter_market_index: 101,
        spot_side: SpotSide::Buy,
        perp_side: PerpSide::Short,
        spot_notional_micros: 25_000_000,
        perp_notional_micros: 25_000_000,
        nav_micros: 10_000_000_000,
        raw_spot_amount: 2_000_000,
        spot_decimals: 6,
        perp_base_amount: 1_000_000,
        perp_base_decimals: 6,
        leverage_micros: 1_000_000,
        created_at_ms: 1_000,
        deadline_ms: 1_500,
        evidence: FrozenEvidence {
            dataset_manifest: "dataset".into(),
            strategy_version: "strategy-v1".into(),
            market_manifest: "market".into(),
            quote_block_hash: "0x01".into(),
            quote_received_at_ms: 900,
            quote_expires_at_ms: 1_500,
            ui_multiplier_e18: 500_000_000_000_000_000,
            estimated_total_cost_micros: 10_000,
        },
    }
}
