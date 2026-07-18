CREATE UNIQUE INDEX agents_one_open_per_user
    ON agents (user_id)
    WHERE status <> 'closed';

CREATE UNIQUE INDEX execution_accounts_one_open_per_user
    ON execution_accounts (user_id)
    WHERE status <> 'closed';

ALTER TABLE coordinator_account_registrations
    DROP CONSTRAINT coordinator_account_registrations_status_check,
    ADD CONSTRAINT coordinator_account_registrations_status_check CHECK (
        status IN ('pending', 'processing', 'registered', 'blocked', 'closed')
    );

UPDATE coordinator_account_registration_outbox outbox SET
    delivered_at = coalesce(outbox.delivered_at, now()),
    claimed_at = NULL,
    claimed_by = NULL,
    updated_at = now()
FROM coordinator_account_registrations registration
WHERE registration.execution_account_id = outbox.execution_account_id
  AND registration.status = 'blocked'
  AND registration.last_error =
      'strategy release changed; registration must not be reused'
  AND registration.strategy_manifest_sha256 IN (
      'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f',
      '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'
  );

WITH terminalized AS (
    UPDATE agent_commands command SET
        status = 'failed',
        error_reason = 'strategy_release_changed_close_required',
        result_evidence_digest = NULL,
        result_owner_actions = '[]'::jsonb,
        completed_at = now(),
        updated_at = now()
    FROM coordinator_account_registrations registration
    WHERE registration.execution_account_id = command.execution_account_id
      AND registration.status = 'blocked'
      AND registration.last_error =
          'strategy release changed; registration must not be reused'
      AND registration.strategy_manifest_sha256 IN (
          'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f',
          '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'
      )
      AND command.status IN ('pending', 'processing', 'awaiting_signature')
      AND (command.command <> 'close' OR registration.registered_at IS NULL)
    RETURNING command.id
), delivered AS (
    UPDATE agent_command_outbox outbox SET
        delivered_at = coalesce(outbox.delivered_at, now()),
        claimed_at = NULL,
        claimed_by = NULL,
        last_error = 'strategy_release_changed_close_required'
    WHERE outbox.command_id IN (SELECT id FROM terminalized)
)
UPDATE agent_commands command SET
    agent_status = 'blocked',
    updated_at = now()
FROM coordinator_account_registrations registration
WHERE registration.execution_account_id = command.execution_account_id
  AND registration.status = 'blocked'
  AND registration.registered_at IS NOT NULL
  AND registration.last_error =
      'strategy release changed; registration must not be reused'
  AND registration.strategy_manifest_sha256 IN (
      'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f',
      '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'
  )
  AND command.command = 'close'
  AND command.status IN ('pending', 'processing', 'awaiting_signature');

ALTER TABLE coordinator_account_registrations
    DROP CONSTRAINT coordinator_registrations_strategy_manifest_v3,
    ADD CONSTRAINT coordinator_registrations_strategy_manifest_v4 CHECK (
        strategy_manifest_sha256 = '7787f323c898f08bec51028ced5ee402f18f85da891515306ee330b2171c3902'
        OR (
            strategy_manifest_sha256 IN (
                'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f',
                '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'
            )
            AND status IN ('blocked', 'closed')
        )
    ) NOT VALID;
