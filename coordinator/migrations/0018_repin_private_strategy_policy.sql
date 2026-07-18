ALTER TABLE execution_account_commands
    ADD CONSTRAINT execution_close_completion_requires_revocation_v1 CHECK (
        command <> 'close'
        OR status <> 'completed'
        OR result @> '{"reconciled_flat":true,"agent_revoked":true}'::jsonb
    ) NOT VALID;

WITH candidates AS MATERIALIZED (
    SELECT command.command_id, command.status AS prior_status
    FROM execution_account_commands command
    JOIN execution_accounts account USING (execution_account_id)
    WHERE account.strategy_version = 'basis-aapl-v1'
      AND account.strategy_manifest_sha256 IN (
          'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f',
          '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'
      )
      AND command.status IN ('processing', 'reducing', 'awaiting_owner_signature')
    FOR UPDATE OF command
),
blocked AS (
    UPDATE execution_account_commands command
    SET status = 'blocked',
        result = jsonb_build_object(
            'control_mode', 'HALTED',
            'reconciled_flat', false,
            'owner_actions', jsonb_build_array(),
            'reason', 'strategy release changed; reconcile and reprovision'
        ),
        updated_at = now()
    FROM candidates
    WHERE command.command_id = candidates.command_id
    RETURNING command.command_id
)
INSERT INTO execution_account_command_events (command_id, status, details)
SELECT candidates.command_id, 'blocked',
       jsonb_build_object(
           'reason', 'strategy release changed; reconcile and reprovision',
           'prior_status', candidates.prior_status
       )
FROM candidates
JOIN blocked USING (command_id);

UPDATE execution_account_control AS control
SET mode = 'HALTED',
    reason = 'strategy release changed; reconcile and reprovision',
    version = version + 1,
    updated_at = now()
FROM execution_accounts account
WHERE account.execution_account_id = control.execution_account_id
  AND account.strategy_version = 'basis-aapl-v1'
  AND account.strategy_manifest_sha256 IN (
      'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f',
      '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'
  )
  AND account.status <> 'closed'
  AND (
      control.mode <> 'HALTED'
      OR control.reason <> 'strategy release changed; reconcile and reprovision'
  );

UPDATE execution_accounts
SET status = 'blocked', updated_at = now()
WHERE strategy_version = 'basis-aapl-v1'
  AND strategy_manifest_sha256 IN (
      'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f',
      '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'
  )
  AND status <> 'closed';

UPDATE execution_strategy_control
SET strategy_manifest_sha256 = '27df8d5a56b45f6966f8a60d866a55cfddfc65835216def5def023126c96c937',
    version = version + 1,
    updated_at = now()
WHERE strategy_version = 'basis-aapl-v1'
  AND strategy_manifest_sha256 IS DISTINCT FROM
      '27df8d5a56b45f6966f8a60d866a55cfddfc65835216def5def023126c96c937';
