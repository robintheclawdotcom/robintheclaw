DO $$
DECLARE
    constraint_name TEXT;
BEGIN
    FOR constraint_name IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'live_scheduler_approvals'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f%'
    LOOP
        EXECUTE format(
            'ALTER TABLE live_scheduler_approvals DROP CONSTRAINT %I',
            constraint_name
        );
    END LOOP;
END;
$$;

WITH candidates AS MATERIALIZED (
    SELECT work.evaluation_id, work.execution_account_id
    FROM live_scheduler_work work
    JOIN live_scheduler_approvals approval
      USING (evaluation_id, execution_account_id)
    WHERE approval.evaluation->>'strategy_manifest_sha256' IN (
        'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f',
        '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'
    )
      AND work.state IN ('pending', 'running', 'quoted', 'ambiguous')
    FOR UPDATE OF work
),
blocked AS (
    UPDATE live_scheduler_work work
    SET state = 'blocked',
        last_error = 'strategy release changed; resubmit under current strategy release',
        lease_owner = NULL,
        lease_until = NULL,
        updated_at = now()
    FROM candidates
    WHERE work.evaluation_id = candidates.evaluation_id
      AND work.execution_account_id = candidates.execution_account_id
    RETURNING work.evaluation_id, work.execution_account_id
)
INSERT INTO live_scheduler_events (
    evaluation_id, execution_account_id, kind, details, details_sha256
)
SELECT blocked.evaluation_id,
       blocked.execution_account_id,
       'blocked',
       jsonb_build_object(
           'reason', 'strategy release changed; resubmit under current strategy release',
           'state', 'blocked'
       ),
       '3e8fdc2161decbb8db3d498a087b2b87040b9929b9447098535a6ae124b019f2'
FROM blocked;

ALTER TABLE live_scheduler_approvals
    ADD CONSTRAINT live_scheduler_manifest_v3 CHECK (
        evaluation->>'strategy_manifest_sha256' = 'c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32'
    ) NOT VALID;
