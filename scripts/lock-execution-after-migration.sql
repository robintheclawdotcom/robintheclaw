SELECT pg_advisory_xact_lock(hashtextextended('robin-execution-release', 0));

UPDATE execution_control
SET mode = 'HALTED',
    reason = 'deployment requires explicit runtime readiness',
    version = version + 1,
    updated_at = now()
WHERE singleton
  AND (mode <> 'HALTED' OR reason <> 'deployment requires explicit runtime readiness');

UPDATE execution_strategy_control
SET mode = 'HALTED',
    reason = 'deployment requires explicit runtime readiness',
    version = version + 1,
    updated_at = now()
WHERE mode <> 'HALTED' OR reason <> 'deployment requires explicit runtime readiness';

UPDATE execution_account_control
SET mode = 'HALTED',
    reason = CASE
        WHEN reason = 'strategy release changed; reconcile and reprovision'
            THEN reason
        ELSE 'deployment requires explicit reconciliation'
    END,
    version = version + 1,
    updated_at = now()
WHERE mode <> 'HALTED'
   OR reason NOT IN (
       'deployment requires explicit reconciliation',
       'strategy release changed; reconcile and reprovision'
   );

UPDATE execution_rollout_readiness
SET alerting_ready = FALSE,
    safe_rotation_ready = FALSE,
    version = version + 1,
    updated_at = now()
WHERE singleton
  AND (alerting_ready OR safe_rotation_ready);

UPDATE execution_account_readiness
SET venue_approved = FALSE,
    oracle_healthy = FALSE,
    sequencer_healthy = FALSE,
    reconciliation_ready = FALSE,
    exit_authority_ready = FALSE,
    version = version + 1,
    updated_at = now()
WHERE venue_approved
   OR oracle_healthy
   OR sequencer_healthy
   OR reconciliation_ready
   OR exit_authority_ready;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM execution_control WHERE singleton AND mode = 'HALTED'
    ) OR EXISTS (
        SELECT 1 FROM execution_strategy_control WHERE mode <> 'HALTED'
    ) OR EXISTS (
        SELECT 1
        FROM execution_account_registrations registration
        LEFT JOIN execution_account_control control USING (execution_account_id)
        WHERE control.execution_account_id IS NULL OR control.mode <> 'HALTED'
    ) THEN
        RAISE EXCEPTION 'all execution controls must be halted before migration release';
    END IF;
    IF EXISTS (SELECT 1 FROM execution_intents WHERE active) THEN
        RAISE EXCEPTION 'execution episodes must be flat before migration release';
    END IF;
    IF EXISTS (
        SELECT 1 FROM execution_actions
        WHERE status IN ('pending', 'leased', 'ambiguous')
    ) THEN
        RAISE EXCEPTION 'execution actions must be terminal before migration release';
    END IF;
    IF EXISTS (
        SELECT 1 FROM live_scheduler_work
        WHERE state IN ('pending', 'running', 'quoted', 'ambiguous')
    ) THEN
        RAISE EXCEPTION 'live scheduler work must be terminal before migration release';
    END IF;
    IF EXISTS (
        SELECT 1 FROM execution_signer_requests
        WHERE status IN ('created', 'ambiguous')
    ) THEN
        RAISE EXCEPTION 'signer requests must be terminal before migration release';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM execution_accounts account
        JOIN execution_account_registrations registration USING (execution_account_id)
        LEFT JOIN LATERAL (
            SELECT snapshot.*
            FROM execution_account_snapshots snapshot
            WHERE snapshot.execution_account_id = account.execution_account_id
              AND snapshot.source = 'lighter-auth'
            ORDER BY snapshot.received_at DESC, snapshot.id DESC
            LIMIT 1
        ) lighter ON TRUE
        LEFT JOIN LATERAL (
            SELECT snapshot.*
            FROM execution_account_snapshots snapshot
            WHERE snapshot.execution_account_id = account.execution_account_id
              AND snapshot.source = 'robinhood-chain'
            ORDER BY snapshot.received_at DESC, snapshot.id DESC
            LIMIT 1
        ) robinhood ON TRUE
        WHERE account.status <> 'closed'
          AND (
              account.agent_id IS DISTINCT FROM registration.agent_id
              OR account.strategy_version IS DISTINCT FROM registration.strategy_version
              OR account.risk_version IS DISTINCT FROM registration.risk_version
              OR account.strategy_manifest_sha256 IS DISTINCT FROM registration.strategy_manifest_sha256
              OR account.lighter_account_index IS DISTINCT FROM registration.lighter_account_index
              OR account.lighter_api_key_index IS DISTINCT FROM registration.lighter_api_key_index
              OR account.owner_address IS DISTINCT FROM registration.robinhood_owner
              OR account.robinhood_vault IS DISTINCT FROM registration.robinhood_vault
              OR account.robinhood_signer IS DISTINCT FROM registration.robinhood_signer
              OR account.binding_sha256 IS DISTINCT FROM registration.binding_sha256
              OR lighter.id IS NULL
              OR lighter.observed_at < now() - interval '5 seconds'
              OR lighter.observed_at > now()
              OR lighter.received_at < now() - interval '5 seconds'
              OR lighter.received_at > now()
              OR lighter.received_at - lighter.observed_at > interval '5 seconds'
              OR lighter.observed_at - lighter.received_at > interval '1 second'
              OR lighter.expires_at <= now()
              OR lighter.expires_at > lighter.received_at + interval '5 seconds'
              OR lighter.payload->>'account_index' IS DISTINCT FROM registration.lighter_account_index::text
              OR lighter.payload->>'api_key_index' IS DISTINCT FROM registration.lighter_api_key_index::text
              OR lighter.payload->>'flat' IS DISTINCT FROM 'true'
              OR lighter.payload->>'no_unknown_orders' IS DISTINCT FROM 'true'
              OR lighter.payload->>'no_unknown_positions' IS DISTINCT FROM 'true'
              OR lighter.payload->>'nonce_aligned' IS DISTINCT FROM 'true'
              OR robinhood.id IS NULL
              OR robinhood.observed_at < now() - interval '5 seconds'
              OR robinhood.observed_at > now()
              OR robinhood.received_at < now() - interval '5 seconds'
              OR robinhood.received_at > now()
              OR robinhood.received_at - robinhood.observed_at > interval '5 seconds'
              OR robinhood.observed_at - robinhood.received_at > interval '1 second'
              OR robinhood.expires_at <= now()
              OR robinhood.expires_at > robinhood.received_at + interval '5 seconds'
              OR robinhood.payload->>'owner_address' IS DISTINCT FROM registration.robinhood_owner
              OR robinhood.payload->>'vault_address' IS DISTINCT FROM registration.robinhood_vault
              OR robinhood.payload->>'signer_address' IS DISTINCT FROM registration.robinhood_signer
              OR robinhood.payload->>'flat' IS DISTINCT FROM 'true'
              OR robinhood.payload->>'nonce_aligned' IS DISTINCT FROM 'true'
              OR robinhood.payload->>'wiring_verified' IS DISTINCT FROM 'true'
              OR robinhood.payload->>'finality_healthy' IS DISTINCT FROM 'true'
          )
    ) THEN
        RAISE EXCEPTION 'registered accounts require fresh authoritative flat snapshots before migration release';
    END IF;
END;
$$;
