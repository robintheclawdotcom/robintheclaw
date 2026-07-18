DROP TRIGGER IF EXISTS execution_promoted_canary_activation ON execution_promotion_events;
DROP FUNCTION IF EXISTS execution_activate_promoted_canary();

UPDATE execution_rollout_readiness
SET alerting_ready = FALSE,
    safe_rotation_ready = FALSE,
    version = version + 1,
    updated_at = now()
WHERE singleton
  AND (alerting_ready OR safe_rotation_ready);

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
