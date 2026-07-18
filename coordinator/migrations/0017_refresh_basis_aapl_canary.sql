DO $$
DECLARE
    uniqueness_constraint TEXT;
    transition_constraint TEXT;
BEGIN
    SELECT conname INTO uniqueness_constraint
    FROM pg_constraint
    WHERE conrelid = 'execution_promotion_events'::regclass
      AND contype = 'u'
      AND pg_get_constraintdef(oid) = 'UNIQUE (strategy_version, to_state)';

    IF uniqueness_constraint IS NULL THEN
        RAISE EXCEPTION 'execution promotion uniqueness constraint is missing';
    END IF;

    EXECUTE format(
        'ALTER TABLE execution_promotion_events DROP CONSTRAINT %I',
        uniqueness_constraint
    );

    SELECT conname INTO transition_constraint
    FROM pg_constraint
    WHERE conrelid = 'execution_promotion_events'::regclass
      AND contype = 'c'
      AND pg_get_constraintdef(oid) LIKE '%from_state%'
      AND pg_get_constraintdef(oid) LIKE '%to_state%';

    IF transition_constraint IS NULL THEN
        RAISE EXCEPTION 'execution promotion transition constraint is missing';
    END IF;

    EXECUTE format(
        'ALTER TABLE execution_promotion_events DROP CONSTRAINT %I',
        transition_constraint
    );
END;
$$;

ALTER TABLE execution_promotion_events
    ADD CONSTRAINT execution_promotion_release_unique
        UNIQUE (strategy_version, evidence_sha256, to_state),
    ADD CONSTRAINT execution_promotion_transition_check CHECK (
        (from_state = 'registered' AND to_state IN ('research', 'canary_eligible', 'rejected', 'retired')) OR
        (from_state = 'research' AND to_state IN ('shadow_eligible', 'canary_eligible', 'rejected', 'retired')) OR
        (from_state = 'shadow_eligible' AND to_state IN ('shadow', 'canary_eligible', 'rejected', 'retired')) OR
        (from_state = 'shadow' AND to_state IN ('audit_ready', 'canary_eligible', 'rejected', 'retired')) OR
        (from_state = 'audit_ready' AND to_state IN ('canary_eligible', 'rejected', 'retired')) OR
        (from_state = 'canary_eligible' AND to_state IN ('canary_eligible', 'retired'))
    );

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
        'internal_audit_sha256', '19e928337af7381e09d0a088e6df02a9b1833533b8c9d8801ed4a7e8fe30a729',
        'internal_audit_approved', TRUE,
        'executor_review_approved', TRUE,
        'key_review_approved', TRUE,
        'restore_drill_approved', TRUE
    ),
    '2a6bc1f8b43d24714e83a478a4c454439bd6cebedca334bd37963924d3ab9711',
    'internal-release-audit-2026-07-17'
) ON CONFLICT (strategy_version, evidence_sha256) DO NOTHING;

DO $$
DECLARE
    current_evidence TEXT;
    current_state TEXT;
BEGIN
    SELECT to_state, evidence_sha256 INTO current_state, current_evidence
    FROM execution_promotion_events
    WHERE strategy_version = 'basis-aapl-v1'
    ORDER BY id DESC
    LIMIT 1;

    IF current_state <> 'canary_eligible' THEN
        RAISE EXCEPTION 'basis-aapl-v1 must already be canary eligible, found %', current_state;
    END IF;
    IF current_evidence = '2a6bc1f8b43d24714e83a478a4c454439bd6cebedca334bd37963924d3ab9711' THEN
        RETURN;
    END IF;

    INSERT INTO execution_promotion_events (
        strategy_version,
        from_state,
        to_state,
        evidence_sha256,
        approved_by
    ) VALUES (
        'basis-aapl-v1',
        'canary_eligible',
        'canary_eligible',
        '2a6bc1f8b43d24714e83a478a4c454439bd6cebedca334bd37963924d3ab9711',
        'internal-release-audit-2026-07-17'
    );
END;
$$;
