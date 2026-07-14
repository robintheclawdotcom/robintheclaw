ALTER TABLE execution_api_nonces
    DROP CONSTRAINT execution_api_nonces_scope_check;

ALTER TABLE execution_api_nonces
    ADD CONSTRAINT execution_api_nonces_scope_check
        CHECK (scope IN (
            'intent', 'exit', 'recovery', 'venue_event', 'market_quote', 'account_snapshot',
            'account_command', 'account_registration', 'open_episode'
        ));

ALTER TABLE execution_market_quotes
    ADD COLUMN unwind_phase TEXT,
    ADD COLUMN perp_unwind_base_amount BIGINT,
    ADD COLUMN perp_unwind_limit_price BIGINT,
    ADD CONSTRAINT execution_market_quotes_executable_exit_check CHECK (
        exit_binding_version <> 2
        OR (
            unwind_phase IN ('perp_and_spot', 'spot_only')
            AND perp_unwind_base_amount BETWEEN 0 AND 9223372036854775807
            AND perp_unwind_limit_price BETWEEN 1 AND 4294967295
            AND (
                (unwind_phase = 'perp_and_spot' AND perp_unwind_base_amount > 0)
                OR (unwind_phase = 'spot_only' AND perp_unwind_base_amount = 0)
            )
        )
    );
