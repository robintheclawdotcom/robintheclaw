ALTER TABLE execution_accounts
    DROP CONSTRAINT execution_accounts_lighter_api_key_index_check,
    ADD CONSTRAINT execution_accounts_lighter_api_key_index_check
        CHECK (lighter_api_key_index BETWEEN 4 AND 254);

ALTER TABLE execution_account_registrations
    DROP CONSTRAINT execution_account_registrations_lighter_api_key_index_check,
    ADD CONSTRAINT execution_account_registrations_lighter_api_key_index_check
        CHECK (lighter_api_key_index BETWEEN 4 AND 254);

ALTER TABLE execution_lighter_nonce_reservations
    DROP CONSTRAINT execution_lighter_nonce_reservations_api_key_index_check,
    ADD CONSTRAINT execution_lighter_nonce_reservations_api_key_index_check
        CHECK (api_key_index BETWEEN 4 AND 254);

ALTER TABLE execution_market_quotes
    DROP CONSTRAINT execution_market_quotes_market_manifest_quote_block_hash_re_key,
    DROP CONSTRAINT execution_market_quotes_exit_fields,
    ADD COLUMN execution_account_id TEXT REFERENCES execution_accounts(execution_account_id),
    ADD COLUMN strategy_manifest_sha256 TEXT
        CHECK (strategy_manifest_sha256 ~ '^[0-9a-f]{64}$'),
    ADD COLUMN route_sha256 TEXT CHECK (route_sha256 ~ '^[0-9a-f]{64}$'),
    ADD COLUMN lighter_market_index INTEGER
        CHECK (lighter_market_index BETWEEN 0 AND 32767),
    ADD COLUMN submission_deadline_ms BIGINT CHECK (submission_deadline_ms > 0),
    ADD COLUMN reconciliation_deadline_ms BIGINT CHECK (reconciliation_deadline_ms > 0),
    ADD COLUMN exit_binding_version SMALLINT NOT NULL DEFAULT 0
        CHECK (exit_binding_version IN (0, 1));

ALTER TABLE execution_market_quotes
    ADD CONSTRAINT execution_market_quotes_exit_fields CHECK (
        (intent_id IS NULL
            AND execution_account_id IS NULL
            AND strategy_manifest_sha256 IS NULL
            AND route_sha256 IS NULL
            AND lighter_market_index IS NULL
            AND spot_unwind_amount_in IS NULL
            AND spot_unwind_expected_amount_out IS NULL
            AND submission_deadline_ms IS NULL
            AND reconciliation_deadline_ms IS NULL
            AND exit_binding_version = 0)
        OR
        (intent_id IS NOT NULL
            AND execution_account_id IS NULL
            AND strategy_manifest_sha256 IS NULL
            AND route_sha256 IS NULL
            AND lighter_market_index IS NULL
            AND spot_unwind_amount_in ~ '^(0|[1-9][0-9]{0,38})$'
            AND spot_unwind_expected_amount_out ~ '^(0|[1-9][0-9]{0,38})$'
            AND submission_deadline_ms IS NULL
            AND reconciliation_deadline_ms IS NULL
            AND exit_binding_version = 0)
        OR
        (intent_id IS NOT NULL
            AND execution_account_id IS NOT NULL
            AND strategy_manifest_sha256 IS NOT NULL
            AND route_sha256 IS NOT NULL
            AND lighter_market_index IS NOT NULL
            AND spot_unwind_amount_in ~ '^(0|[1-9][0-9]{0,38})$'
            AND spot_unwind_expected_amount_out ~ '^(0|[1-9][0-9]{0,38})$'
            AND submission_deadline_ms IS NOT NULL
            AND reconciliation_deadline_ms > submission_deadline_ms
            AND reconciliation_deadline_ms - submission_deadline_ms <= 86400000
            AND exit_binding_version = 1)
    );

CREATE TABLE execution_exit_requests (
    request_id TEXT PRIMARY KEY CHECK (request_id ~ '^0x[0-9a-f]{64}$'),
    intent_id TEXT NOT NULL REFERENCES execution_intents(id),
    execution_account_id TEXT NOT NULL REFERENCES execution_accounts(execution_account_id),
    quote_source TEXT NOT NULL DEFAULT 'execution-authority'
        CHECK (quote_source = 'execution-authority'),
    quote_source_session TEXT NOT NULL,
    quote_source_event_id TEXT NOT NULL,
    quote_payload_sha256 TEXT NOT NULL CHECK (quote_payload_sha256 ~ '^[0-9a-f]{64}$'),
    payload JSONB NOT NULL,
    payload_sha256 TEXT NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    saga_version_at_accept BIGINT NOT NULL CHECK (saga_version_at_accept > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (quote_source, quote_source_session, quote_source_event_id)
        REFERENCES execution_market_quotes(source, source_session, source_event_id)
);

CREATE INDEX execution_exit_requests_intent
    ON execution_exit_requests (intent_id, created_at DESC);

CREATE TRIGGER execution_exit_requests_append_only
    BEFORE UPDATE OR DELETE ON execution_exit_requests
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();
