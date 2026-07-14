ALTER TABLE execution_market_quotes
    DROP CONSTRAINT execution_market_quotes_exit_binding_version_check,
    DROP CONSTRAINT execution_market_quotes_executable_exit_check,
	DROP CONSTRAINT execution_market_quotes_exit_fields,
    ADD COLUMN expected_ui_multiplier TEXT,
    ADD COLUMN min_oracle_round_id TEXT,
    ADD CONSTRAINT execution_market_quotes_exit_binding_version_check
        CHECK (exit_binding_version IN (0, 1, 2, 3)),
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
			AND exit_binding_version IN (1, 2, 3))
	),
    ADD CONSTRAINT execution_market_quotes_execution_policy_check CHECK (
        (expected_ui_multiplier IS NULL AND min_oracle_round_id IS NULL
            AND exit_binding_version <> 3)
        OR
        (expected_ui_multiplier ~ '^[1-9][0-9]{0,77}$'
            AND (
                length(expected_ui_multiplier) < 78
                OR expected_ui_multiplier <=
                   '115792089237316195423570985008687907853269984665640564039457584007913129639935'
            )
            AND min_oracle_round_id ~ '^[1-9][0-9]{0,24}$'
            AND (
                length(min_oracle_round_id) < 25
                OR min_oracle_round_id <= '1208925819614629174706175'
            ))
    ),
    ADD CONSTRAINT execution_market_quotes_executable_exit_check CHECK (
        exit_binding_version NOT IN (2, 3)
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
