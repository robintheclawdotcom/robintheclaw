ALTER TABLE execution_market_quotes
    DROP CONSTRAINT execution_market_quotes_source_check;

ALTER TABLE execution_market_quotes
    ADD CONSTRAINT execution_market_quotes_source_check
        CHECK (source IN ('lighter-auth', 'robinhood-chain', 'execution-authority')),
    ADD COLUMN intent_id TEXT REFERENCES execution_intents(id),
    ADD COLUMN spot_unwind_amount_in TEXT,
    ADD COLUMN spot_unwind_expected_amount_out TEXT,
    ADD CONSTRAINT execution_market_quotes_exit_fields CHECK (
        (intent_id IS NULL
            AND spot_unwind_amount_in IS NULL
            AND spot_unwind_expected_amount_out IS NULL)
        OR
        (intent_id IS NOT NULL
            AND spot_unwind_amount_in ~ '^(0|[1-9][0-9]{0,38})$'
            AND spot_unwind_expected_amount_out ~ '^(0|[1-9][0-9]{0,38})$')
    );

CREATE INDEX execution_market_quotes_exit_lookup
    ON execution_market_quotes (intent_id, expires_at DESC, id DESC)
    WHERE intent_id IS NOT NULL;

ALTER TABLE execution_api_nonces
    DROP CONSTRAINT execution_api_nonces_scope_check;

ALTER TABLE execution_api_nonces
    ADD CONSTRAINT execution_api_nonces_scope_check
        CHECK (scope IN ('intent', 'exit', 'recovery', 'venue_event', 'market_quote'));
