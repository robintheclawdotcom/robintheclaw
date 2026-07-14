INSERT INTO execution_promotion_evidence (
    strategy_version,
    evidence,
    evidence_sha256,
    approved_by
) VALUES (
    'basis-aapl-v1',
    jsonb_build_object(
        'approval_type', 'engineering_canary',
        'max_accounts', 1,
        'max_leg_notional_micros', 25000000,
        'max_gross_notional_micros', 50000000,
        'max_daily_turnover_micros', 50000000,
        'max_leverage_ppm', 1000000,
        'internal_audit_sha256', 'd271fbd0579d1e2753cf5d1e763a4973beb18552426c309994d56e3f81d8f39c',
        'internal_audit_approved', TRUE,
        'executor_review_approved', TRUE,
        'key_review_approved', TRUE,
        'restore_drill_approved', TRUE
    ),
    '7be8be449e9897075e9ab9f0e7d6fb26b9140fe5ae568adf696fca8c7bb31c2a',
    'internal-release-audit-2026-07-14'
) ON CONFLICT (strategy_version, evidence_sha256) DO NOTHING;

DO $$
DECLARE
    current_state TEXT;
BEGIN
    SELECT to_state INTO current_state
    FROM execution_promotion_events
    WHERE strategy_version = 'basis-aapl-v1'
    ORDER BY id DESC
    LIMIT 1;

    IF current_state = 'canary_eligible' THEN
        IF EXISTS (
            SELECT 1
            FROM execution_promotion_events
            WHERE strategy_version = 'basis-aapl-v1'
              AND evidence_sha256 = '7be8be449e9897075e9ab9f0e7d6fb26b9140fe5ae568adf696fca8c7bb31c2a'
              AND id = (
                  SELECT max(id)
                  FROM execution_promotion_events
                  WHERE strategy_version = 'basis-aapl-v1'
              )
        ) THEN
            RETURN;
        END IF;
        RAISE EXCEPTION 'basis-aapl-v1 is already promoted with different evidence';
    END IF;
    IF current_state IN ('rejected', 'retired') THEN
        RAISE EXCEPTION 'basis-aapl-v1 has terminal promotion state %', current_state;
    END IF;

    INSERT INTO execution_promotion_events (
        strategy_version,
        from_state,
        to_state,
        evidence_sha256,
        approved_by
    ) VALUES (
        'basis-aapl-v1',
        COALESCE(current_state, 'registered'),
        'canary_eligible',
        '7be8be449e9897075e9ab9f0e7d6fb26b9140fe5ae568adf696fca8c7bb31c2a',
        'internal-release-audit-2026-07-14'
    );
END;
$$;
