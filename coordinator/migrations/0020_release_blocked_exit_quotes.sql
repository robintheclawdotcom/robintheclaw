CREATE VIEW execution_account_observation_candidates_v1
WITH (security_barrier = true) AS
SELECT
    account.execution_account_id,
    account.agent_id,
    account.strategy_manifest_sha256 AS target_strategy_manifest_sha256,
    account.status = 'blocked' AS release_recovery
FROM execution_accounts account
JOIN execution_account_registrations registration USING (execution_account_id)
JOIN execution_account_control account_control USING (execution_account_id)
JOIN execution_strategy_control strategy_control
  ON strategy_control.strategy_version = account.strategy_version
WHERE account.agent_id = registration.agent_id
  AND account.strategy_version = registration.strategy_version
  AND account.risk_version = registration.risk_version
  AND account.strategy_manifest_sha256 = registration.strategy_manifest_sha256
  AND account.lighter_account_index = registration.lighter_account_index
  AND account.lighter_api_key_index = registration.lighter_api_key_index
  AND account.robinhood_vault = registration.robinhood_vault
  AND account.robinhood_signer = registration.robinhood_signer
  AND account.owner_address = registration.robinhood_owner
  AND account.binding_sha256 = registration.binding_sha256
  AND (
      (
          account.status = 'active'
          AND account.strategy_manifest_sha256 =
              'c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32'
          AND strategy_control.strategy_manifest_sha256 =
              account.strategy_manifest_sha256
      )
      OR
      (
          account.status = 'blocked'
          AND account.strategy_manifest_sha256 IN (
              'da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f',
              '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'
          )
          AND account_control.mode = 'HALTED'
          AND account_control.reason =
              'strategy release changed; reconcile and reprovision'
          AND strategy_control.strategy_manifest_sha256 =
              'c413f56adcabd679b600fc5df8e660ab7684aaa372ea84db135b586cce687c32'
      )
  );

CREATE VIEW execution_exit_quote_candidates_v1
WITH (security_barrier = true) AS
SELECT
    action.id AS action_id,
    intent.id AS intent_id,
    intent.execution_account_id,
    candidate.target_strategy_manifest_sha256,
    command.command_id,
    command.command,
    candidate.release_recovery
FROM execution_actions action
JOIN execution_intents intent
  ON intent.id = action.intent_id
 AND intent.active
JOIN execution_account_observation_candidates_v1 candidate
  USING (execution_account_id)
JOIN execution_account_commands command
  ON command.command_id = action.payload->>'control_command_id'
 AND command.execution_account_id = intent.execution_account_id
 AND command.agent_id = candidate.agent_id
 AND command.status = 'reducing'
WHERE action.kind IN ('unwind_perp', 'unwind_spot')
  AND action.status IN ('pending', 'leased')
  AND action.payload->>'exit_reason' = 'operator_exit'
  AND CASE
      WHEN action.payload->>'authority_wait_deadline_ms' ~ '^[0-9]{1,20}$'
      THEN (action.payload->>'authority_wait_deadline_ms')::numeric <=
           18446744073709551615
      ELSE false
  END
  AND intent.payload->>'strategy_manifest_sha256' =
      candidate.target_strategy_manifest_sha256
  AND (
      (
          NOT candidate.release_recovery
          AND command.command IN ('pause', 'close')
      )
      OR
      (
          candidate.release_recovery
          AND command.command = 'close'
      )
  );
