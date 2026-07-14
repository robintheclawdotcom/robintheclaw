CREATE OR REPLACE FUNCTION live_scheduler_reject_mutation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'live scheduler journal is append-only';
END;
$$;

CREATE OR REPLACE FUNCTION live_scheduler_exact_keys(document JSONB, expected TEXT[])
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE(array_agg(key ORDER BY key), ARRAY[]::TEXT[]) =
           (SELECT array_agg(value ORDER BY value) FROM unnest(expected) AS valueset(value))
    FROM jsonb_object_keys(document) AS keys(key);
$$;

CREATE TABLE live_scheduler_approvals (
    evaluation_id TEXT NOT NULL CHECK (evaluation_id ~ '^0x[0-9a-f]{64}$'),
    execution_account_id TEXT NOT NULL REFERENCES execution_accounts(execution_account_id),
    agent_id TEXT NOT NULL CHECK (agent_id ~ '^[a-z0-9][a-z0-9-]{7,63}$'),
    evaluation JSONB NOT NULL,
    readiness JSONB NOT NULL,
    account_state JSONB NOT NULL,
    approval_sha256 TEXT NOT NULL CHECK (approval_sha256 ~ '^[0-9a-f]{64}$'),
    approved_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (evaluation_id, execution_account_id),
    CHECK (expires_at > approved_at),
    CHECK (evaluation_id = evaluation->>'id'),
    CHECK (execution_account_id = readiness->>'execution_account_id'),
    CHECK (execution_account_id = account_state->>'execution_account_id'),
    CHECK (agent_id = readiness->>'agent_id'),
    CHECK (agent_id = account_state->>'agent_id'),
    CHECK (evaluation->>'strategy_version' = 'basis-aapl-v1'),
    CHECK (evaluation->>'strategy_manifest_sha256' = '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'),
    CHECK (evaluation->>'source_config_sha256' = 'b701b39cbce20ccef48527811299732812d14297750fc3eee2a3c4a4a3f29edd'),
    CHECK (evaluation->>'status' = 'approved'),
	CHECK (
		(evaluation->>'action' = 'entry'
		 AND evaluation->>'pair_intent_id' = '')
		OR
		(evaluation->>'action' = 'unwind'
		 AND evaluation->>'pair_intent_id' ~ '^0x[0-9a-f]{64}$')
	),
	CHECK (evaluation->>'source_episode_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
	CHECK (evaluation->>'paper_evaluation_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CHECK (live_scheduler_exact_keys(evaluation, ARRAY[
        'id', 'strategy_version', 'strategy_manifest_sha256', 'source_config_sha256',
        'dataset_manifest', 'market_manifest', 'status', 'action', 'observed_at_ms',
		'estimated_cost_micros', 'source_episode_id', 'paper_evaluation_id',
		'pair_intent_id'
    ])),
    CHECK (live_scheduler_exact_keys(readiness, ARRAY[
        'execution_account_id', 'agent_id', 'strategy_version', 'strategy_manifest_sha256',
        'lifecycle', 'global_control', 'strategy_control', 'account_control', 'fully_verified',
        'vault_wired', 'vault_funded', 'execution_signer_funded', 'lighter_linked',
        'lighter_funded', 'route_healthy', 'oracle_healthy', 'sequencer_healthy', 'observed_at_ms'
    ])),
    CHECK (live_scheduler_exact_keys(account_state, ARRAY[
        'execution_account_id', 'agent_id', 'strategy_manifest_sha256', 'lighter_account_index',
        'lighter_api_key_index', 'lighter_market_index', 'lighter_nonce_aligned',
        'unknown_lighter_orders', 'unknown_lighter_positions', 'collateral_micros',
        'maintenance_margin_micros', 'robinhood_vault', 'robinhood_signer',
        'robinhood_nonce_aligned', 'unknown_robinhood_position', 'nav_micros',
        'daily_turnover_micros', 'active_episodes', 'flat', 'spot_decimals',
        'spot_config_version', 'ui_multiplier_e18', 'next_client_order_index',
        'next_unwind_order_index', 'observed_at_ms'
    ]))
);

CREATE TRIGGER live_scheduler_approvals_append_only
    BEFORE UPDATE OR DELETE ON live_scheduler_approvals
    FOR EACH ROW EXECUTE FUNCTION live_scheduler_reject_mutation();

CREATE TABLE live_scheduler_work (
    evaluation_id TEXT NOT NULL,
    execution_account_id TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'running', 'quoted', 'ambiguous', 'succeeded', 'blocked')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    request_id TEXT CHECK (request_id ~ '^0x[0-9a-f]{64}$'),
    requested_at_ms BIGINT CHECK (requested_at_ms > 0),
    quote_body BYTEA,
    quote_sha256 TEXT CHECK (quote_sha256 ~ '^[0-9a-f]{64}$'),
    runner_body BYTEA,
    runner_sha256 TEXT CHECK (runner_sha256 ~ '^[0-9a-f]{64}$'),
    outcome_body BYTEA,
    outcome_sha256 TEXT CHECK (outcome_sha256 ~ '^[0-9a-f]{64}$'),
    last_error TEXT CHECK (length(last_error) <= 240),
    lease_owner TEXT CHECK (lease_owner ~ '^[a-z0-9][a-z0-9-]{7,63}$'),
    lease_until TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (evaluation_id, execution_account_id),
    FOREIGN KEY (evaluation_id, execution_account_id)
        REFERENCES live_scheduler_approvals(evaluation_id, execution_account_id),
    CHECK ((request_id IS NULL) = (requested_at_ms IS NULL)),
    CHECK ((quote_body IS NULL) = (quote_sha256 IS NULL)),
    CHECK ((runner_body IS NULL) = (runner_sha256 IS NULL)),
    CHECK ((outcome_body IS NULL) = (outcome_sha256 IS NULL)),
    CHECK ((lease_owner IS NULL) = (lease_until IS NULL))
);

CREATE INDEX live_scheduler_work_claim
    ON live_scheduler_work (state, lease_until, evaluation_id, execution_account_id)
    WHERE state IN ('pending', 'running', 'quoted', 'ambiguous');

CREATE TABLE live_scheduler_events (
    id BIGSERIAL PRIMARY KEY,
    evaluation_id TEXT NOT NULL,
    execution_account_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN (
        'claimed', 'quote_prepared', 'quote_persisted', 'runner_prepared',
        'runner_ambiguous', 'retry_scheduled', 'completed', 'blocked'
    )),
    details JSONB NOT NULL,
    details_sha256 TEXT NOT NULL CHECK (details_sha256 ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (evaluation_id, execution_account_id)
        REFERENCES live_scheduler_approvals(evaluation_id, execution_account_id)
);

CREATE TRIGGER live_scheduler_events_append_only
    BEFORE UPDATE OR DELETE ON live_scheduler_events
    FOR EACH ROW EXECUTE FUNCTION live_scheduler_reject_mutation();

CREATE OR REPLACE FUNCTION live_scheduler_create_work()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO live_scheduler_work (evaluation_id, execution_account_id)
    VALUES (NEW.evaluation_id, NEW.execution_account_id);
    RETURN NEW;
END;
$$;

CREATE TRIGGER live_scheduler_approval_work
    AFTER INSERT ON live_scheduler_approvals
    FOR EACH ROW EXECUTE FUNCTION live_scheduler_create_work();
