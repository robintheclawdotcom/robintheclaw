CREATE TABLE execution_rollout_readiness (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    alerting_ready BOOLEAN NOT NULL,
    safe_rotation_ready BOOLEAN NOT NULL,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO execution_rollout_readiness (
    singleton, alerting_ready, safe_rotation_ready
) VALUES (
    TRUE, TRUE, TRUE
);

COMMENT ON COLUMN execution_account_readiness.venue_approved IS
    'Derived canonical venue binding and identity verification.';
COMMENT ON COLUMN execution_account_readiness.alerting_ready IS
    'Deprecated. Rollout alerting readiness is stored in execution_rollout_readiness.';
COMMENT ON COLUMN execution_account_readiness.safe_rotation_ready IS
    'Deprecated. Rollout key-rotation readiness is stored in execution_rollout_readiness.';

CREATE OR REPLACE FUNCTION execution_snapshot_exact_keys(document JSONB, expected TEXT[])
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE(array_agg(key ORDER BY key), ARRAY[]::TEXT[]) =
           (SELECT array_agg(value ORDER BY value) FROM unnest(expected) AS valueset(value))
    FROM jsonb_object_keys(document) AS keys(key);
$$;

ALTER TABLE execution_account_snapshots
    ADD CONSTRAINT execution_lighter_snapshot_v2 CHECK (
        source <> 'lighter-auth' OR execution_snapshot_exact_keys(payload, ARRAY[
            'account_index', 'api_key_index', 'market_index', 'nonce_aligned',
            'no_unknown_orders', 'no_unknown_positions', 'collateral_ready',
            'maintenance_margin_ratio_micros', 'collateral_micros',
            'maintenance_margin_micros', 'flat'
        ])
    ) NOT VALID,
    ADD CONSTRAINT execution_robinhood_snapshot_v2 CHECK (
        source <> 'robinhood-chain' OR execution_snapshot_exact_keys(payload, ARRAY[
            'vault_address', 'signer_address', 'funding_ready', 'wiring_verified',
            'finality_healthy', 'flat', 'owner_address', 'agent_enabled', 'risk_mode',
            'settlement_balance_raw', 'nonce_aligned', 'spot_config_version',
            'stock_decimals', 'ui_multiplier_e18', 'new_ui_multiplier_e18',
            'oracle_paused', 'oracle_healthy', 'sequencer_healthy', 'signer_gas_ready'
        ])
    ) NOT VALID;

WITH promoted AS (
    SELECT DISTINCT ON (strategy_version) strategy_version, to_state
    FROM execution_promotion_events
    ORDER BY strategy_version, id DESC
)
UPDATE execution_strategy_control AS control
SET mode = 'ACTIVE', reason = 'promoted canary strategy', version = version + 1,
    updated_at = now()
FROM promoted
WHERE promoted.strategy_version = control.strategy_version
  AND promoted.to_state = 'canary_eligible'
  AND control.mode = 'HALTED'
  AND control.reason = 'strategy activation requires explicit approval';

UPDATE execution_control
SET mode = 'ACTIVE', reason = 'promoted canary execution', version = version + 1,
    updated_at = now()
WHERE singleton AND mode = 'HALTED' AND reason IN (
    'initial deployment',
    'multi-account migration requires explicit readiness'
)
AND EXISTS (
    SELECT 1
    FROM execution_promotion_events event
    WHERE event.to_state = 'canary_eligible'
      AND event.id = (
          SELECT max(latest.id)
          FROM execution_promotion_events latest
          WHERE latest.strategy_version = event.strategy_version
      )
);

UPDATE execution_account_control AS control
SET mode = 'REDUCE_ONLY', reason = 'awaiting fresh derived readiness',
    version = version + 1, updated_at = now()
FROM execution_accounts account
JOIN execution_account_registrations registration USING (execution_account_id)
WHERE account.execution_account_id = control.execution_account_id
  AND account.status = 'active'
  AND control.mode = 'HALTED'
  AND control.reason IN (
      'registration requires verified readiness and activation',
      'migration requires explicit account binding'
  );

CREATE OR REPLACE FUNCTION execution_activate_promoted_canary()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.to_state <> 'canary_eligible' THEN
        RETURN NEW;
    END IF;
    UPDATE execution_strategy_control
    SET mode = 'ACTIVE', reason = 'promoted canary strategy', version = version + 1,
        updated_at = now()
    WHERE strategy_version = NEW.strategy_version
      AND mode = 'HALTED'
      AND reason = 'strategy activation requires explicit approval';
    UPDATE execution_control
    SET mode = 'ACTIVE', reason = 'promoted canary execution', version = version + 1,
        updated_at = now()
    WHERE singleton AND mode = 'HALTED' AND reason IN (
        'initial deployment',
        'multi-account migration requires explicit readiness'
    );
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_promoted_canary_activation
    AFTER INSERT ON execution_promotion_events
    FOR EACH ROW EXECUTE FUNCTION execution_activate_promoted_canary();
