ALTER TABLE execution_account_snapshots
    DROP CONSTRAINT execution_robinhood_snapshot_v2,
    ADD CONSTRAINT execution_robinhood_snapshot_v4 CHECK (
        source <> 'robinhood-chain' OR execution_snapshot_exact_keys(payload, ARRAY[
            'vault_address', 'signer_address', 'funding_ready', 'wiring_verified',
            'finality_healthy', 'flat', 'owner_address', 'agent_enabled', 'risk_mode',
            'finalized_agent_address', 'finalized_agent_enabled', 'finalized_agent_revoked',
            'global_mode', 'finalized_global_mode', 'finalized_risk_mode',
            'settlement_balance_raw', 'nonce_aligned', 'spot_config_version',
            'stock_decimals', 'ui_multiplier_e18', 'new_ui_multiplier_e18',
            'oracle_paused', 'oracle_healthy', 'sequencer_healthy', 'signer_gas_ready',
            'finalized_number', 'finalized_hash', 'finalized_timestamp',
            'source_block_number', 'source_block_hash', 'source_block_timestamp'
        ])
    ) NOT VALID;
