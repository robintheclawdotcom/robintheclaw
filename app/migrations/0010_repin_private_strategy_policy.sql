DO $$
DECLARE
    constraint_record RECORD;
BEGIN
    FOR constraint_record IN
        SELECT conname, conrelid::regclass AS table_name
        FROM pg_constraint
        WHERE conrelid IN (
            'execution_accounts'::regclass,
            'coordinator_account_registrations'::regclass
        )
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f%'
    LOOP
        EXECUTE format(
            'ALTER TABLE %s DROP CONSTRAINT %I',
            constraint_record.table_name,
            constraint_record.conname
        );
    END LOOP;
END;
$$;

UPDATE execution_accounts AS account
SET strategy_manifest_sha256 = '7787f323c898f08bec51028ced5ee402f18f85da891515306ee330b2171c3902',
    updated_at = now()
WHERE account.strategy_manifest_sha256 = 'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f'
  AND NOT EXISTS (
      SELECT 1
      FROM execution_account_bindings binding
      WHERE binding.execution_account_id = account.id
        AND binding.status <> 'rejected'
  )
  AND NOT EXISTS (
      SELECT 1
      FROM coordinator_account_registrations registration
      WHERE registration.execution_account_id = account.id
  );

UPDATE agents AS agent
SET status = 'blocked', blocked_reason = 'strategy release changed; reconcile before reprovisioning',
    updated_at = now()
FROM execution_accounts account
WHERE account.agent_id = agent.id
  AND account.strategy_manifest_sha256 = 'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f'
  AND agent.status <> 'closed';

UPDATE execution_accounts
SET status = 'blocked', updated_at = now()
WHERE strategy_manifest_sha256 = 'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f'
  AND status <> 'closed';

UPDATE coordinator_account_registrations
SET status = 'blocked', last_error = 'strategy release changed; registration must not be reused',
    updated_at = now()
WHERE strategy_manifest_sha256 = 'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f';

ALTER TABLE execution_accounts
    ADD CONSTRAINT execution_accounts_strategy_manifest_v3 CHECK (
        strategy_manifest_sha256 = '7787f323c898f08bec51028ced5ee402f18f85da891515306ee330b2171c3902'
        OR (
            strategy_manifest_sha256 IN (
                'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f',
                '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'
            )
            AND status IN ('blocked', 'closed')
        )
    ) NOT VALID;

ALTER TABLE coordinator_account_registrations
    ADD CONSTRAINT coordinator_registrations_strategy_manifest_v3 CHECK (
        strategy_manifest_sha256 = '7787f323c898f08bec51028ced5ee402f18f85da891515306ee330b2171c3902'
        OR (
            strategy_manifest_sha256 IN (
                'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f',
                '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'
            )
            AND status = 'blocked'
        )
    ) NOT VALID;
