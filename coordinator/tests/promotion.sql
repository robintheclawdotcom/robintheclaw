\set ON_ERROR_STOP on

INSERT INTO execution_promotion_evidence
    (strategy_version, evidence, evidence_sha256, approved_by)
VALUES
    ('strategy-v1', '{}', repeat('a', 64), 'approval-record');

DO $$
BEGIN
    BEGIN
        INSERT INTO execution_promotion_events
            (strategy_version, from_state, to_state, evidence_sha256, approved_by)
        VALUES
            ('strategy-v1', 'registered', 'shadow', repeat('a', 64), 'approval-record');
        RAISE EXCEPTION 'skipped promotion was accepted';
    EXCEPTION
        WHEN check_violation THEN NULL;
    END;
END;
$$;

INSERT INTO execution_promotion_events
    (strategy_version, from_state, to_state, evidence_sha256, approved_by)
VALUES
    ('strategy-v1', 'registered', 'research', repeat('a', 64), 'approval-record'),
    ('strategy-v1', 'research', 'shadow_eligible', repeat('a', 64), 'approval-record'),
    ('strategy-v1', 'shadow_eligible', 'shadow', repeat('a', 64), 'approval-record'),
    ('strategy-v1', 'shadow', 'audit_ready', repeat('a', 64), 'approval-record'),
    ('strategy-v1', 'audit_ready', 'canary_eligible', repeat('a', 64), 'approval-record');

DO $$
DECLARE
    latest TEXT;
BEGIN
    SELECT to_state INTO latest
    FROM execution_promotion_events
    WHERE strategy_version = 'strategy-v1'
    ORDER BY id DESC
    LIMIT 1;
    IF latest <> 'canary_eligible' THEN
        RAISE EXCEPTION 'unexpected final promotion state: %', latest;
    END IF;

    BEGIN
        UPDATE execution_promotion_events SET approved_by = 'changed' WHERE id = 1;
        RAISE EXCEPTION 'append-only promotion was updated';
    EXCEPTION
        WHEN raise_exception THEN
            IF SQLERRM NOT LIKE '%append-only%' THEN
                RAISE;
            END IF;
    END;
END;
$$;
