DO $$
DECLARE
    constraint_name TEXT;
BEGIN
    FOR constraint_name IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'live_scheduler_approvals'::regclass
          AND contype = 'c'
          AND (
              pg_get_constraintdef(oid) LIKE '%4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a%'
              OR pg_get_constraintdef(oid) LIKE '%b701b39cbce20ccef48527811299732812d14297750fc3eee2a3c4a4a3f29edd%'
          )
    LOOP
        EXECUTE format(
            'ALTER TABLE live_scheduler_approvals DROP CONSTRAINT %I',
            constraint_name
        );
    END LOOP;
END;
$$;

ALTER TABLE live_scheduler_approvals
    ADD CONSTRAINT live_scheduler_manifest_v2 CHECK (
        evaluation->>'strategy_manifest_sha256' = 'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f'
    ) NOT VALID,
    ADD CONSTRAINT live_scheduler_source_config_v2 CHECK (
        evaluation->>'source_config_sha256' = '59106a18758a95af45e6ac1a8257843cfbd2a45fd09b5b3c3f429d3dedb56c2a'
    ) NOT VALID;
