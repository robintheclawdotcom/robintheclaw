ALTER TABLE live_scheduler_approvals
    ADD COLUMN approval_version SMALLINT NOT NULL DEFAULT 1;

ALTER TABLE live_scheduler_approvals
    ALTER COLUMN approval_version SET DEFAULT 2;

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
              lower(pg_get_constraintdef(oid)) LIKE '%live_scheduler_exact_keys(evaluation%'
              OR (
                  lower(pg_get_constraintdef(oid)) LIKE '%action%'
                  AND lower(pg_get_constraintdef(oid)) LIKE '%entry%'
              )
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
    ADD CONSTRAINT live_scheduler_approval_version_check
        CHECK (approval_version IN (1, 2)),
    ADD CONSTRAINT live_scheduler_evaluation_keys_v2 CHECK (
        (approval_version = 1 AND live_scheduler_exact_keys(evaluation, ARRAY[
            'id', 'strategy_version', 'strategy_manifest_sha256', 'source_config_sha256',
            'dataset_manifest', 'market_manifest', 'status', 'action', 'observed_at_ms',
            'estimated_cost_micros'
        ]))
        OR
        (approval_version = 2 AND live_scheduler_exact_keys(evaluation, ARRAY[
            'id', 'strategy_version', 'strategy_manifest_sha256', 'source_config_sha256',
            'dataset_manifest', 'market_manifest', 'status', 'action', 'observed_at_ms',
            'estimated_cost_micros', 'source_episode_id', 'paper_evaluation_id',
            'pair_intent_id'
        ]))
    ),
    ADD CONSTRAINT live_scheduler_evaluation_binding_v2 CHECK (
        (approval_version = 1 AND evaluation->>'action' = 'entry')
        OR
        (approval_version = 2
            AND evaluation->>'source_episode_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
            AND evaluation->>'paper_evaluation_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
            AND (
                (evaluation->>'action' = 'entry' AND evaluation->>'pair_intent_id' = '')
                OR
                (evaluation->>'action' = 'unwind'
                    AND evaluation->>'pair_intent_id' ~ '^0x[0-9a-f]{64}$')
            ))
    );

CREATE TABLE live_execution_episode_bindings (
    execution_account_id TEXT NOT NULL
        REFERENCES execution_accounts(execution_account_id),
    source_episode_id UUID NOT NULL,
    source_entry_evaluation_id UUID NOT NULL,
    entry_evaluation_id TEXT NOT NULL,
    intent_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    market_manifest TEXT NOT NULL CHECK (market_manifest ~ '^0x[0-9a-f]{64}$'),
    bound_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (execution_account_id, source_episode_id),
    UNIQUE (intent_id),
    UNIQUE (entry_evaluation_id, execution_account_id),
    FOREIGN KEY (entry_evaluation_id, execution_account_id)
        REFERENCES live_scheduler_approvals(evaluation_id, execution_account_id),
    FOREIGN KEY (intent_id, execution_account_id)
        REFERENCES execution_intents(id, execution_account_id)
);

CREATE TRIGGER live_execution_episode_bindings_append_only
    BEFORE UPDATE OR DELETE ON live_execution_episode_bindings
    FOR EACH ROW EXECUTE FUNCTION live_scheduler_reject_mutation();

CREATE TABLE live_strategy_exit_bindings (
    exit_evaluation_id TEXT NOT NULL,
    execution_account_id TEXT NOT NULL,
    intent_id TEXT NOT NULL,
    source_episode_id UUID NOT NULL,
    source_close_evaluation_id UUID NOT NULL,
    close_reason TEXT NOT NULL CHECK (length(close_reason) BETWEEN 1 AND 128),
    approved_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (exit_evaluation_id, execution_account_id),
    UNIQUE (intent_id),
    FOREIGN KEY (exit_evaluation_id, execution_account_id)
        REFERENCES live_scheduler_approvals(evaluation_id, execution_account_id),
    FOREIGN KEY (execution_account_id, source_episode_id)
        REFERENCES live_execution_episode_bindings(execution_account_id, source_episode_id),
    FOREIGN KEY (intent_id, execution_account_id)
        REFERENCES execution_intents(id, execution_account_id)
);

CREATE TRIGGER live_strategy_exit_bindings_append_only
    BEFORE UPDATE OR DELETE ON live_strategy_exit_bindings
    FOR EACH ROW EXECUTE FUNCTION live_scheduler_reject_mutation();
