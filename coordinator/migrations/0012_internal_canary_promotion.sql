DO $$
DECLARE
    transition_constraint TEXT;
BEGIN
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
    ADD CONSTRAINT execution_promotion_transition_check CHECK (
        (from_state = 'registered' AND to_state IN ('research', 'canary_eligible', 'rejected', 'retired')) OR
        (from_state = 'research' AND to_state IN ('shadow_eligible', 'canary_eligible', 'rejected', 'retired')) OR
        (from_state = 'shadow_eligible' AND to_state IN ('shadow', 'canary_eligible', 'rejected', 'retired')) OR
        (from_state = 'shadow' AND to_state IN ('audit_ready', 'canary_eligible', 'rejected', 'retired')) OR
        (from_state = 'audit_ready' AND to_state IN ('canary_eligible', 'rejected', 'retired')) OR
        (from_state = 'canary_eligible' AND to_state = 'retired')
    );
