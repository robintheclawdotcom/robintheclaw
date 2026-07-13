ALTER TABLE execution_api_nonces
    DROP CONSTRAINT execution_api_nonces_scope_check;

ALTER TABLE execution_api_nonces
    ADD CONSTRAINT execution_api_nonces_scope_check
    CHECK (scope IN ('intent', 'exit', 'venue_event', 'market_quote'));

CREATE TABLE execution_market_configs (
    manifest_id TEXT PRIMARY KEY CHECK (manifest_id ~ '^0x[0-9a-f]{64}$'),
    symbol TEXT NOT NULL CHECK (symbol ~ '^[A-Z0-9._-]{1,32}$'),
    spot_token TEXT NOT NULL CHECK (spot_token ~ '^0x[0-9a-f]{40}$'),
    lighter_market_index INTEGER NOT NULL CHECK (lighter_market_index BETWEEN 0 AND 32767),
    spot_decimals SMALLINT NOT NULL CHECK (spot_decimals BETWEEN 0 AND 38),
    perp_base_decimals SMALLINT NOT NULL CHECK (perp_base_decimals BETWEEN 0 AND 18),
    perp_price_decimals SMALLINT NOT NULL CHECK (perp_price_decimals BETWEEN 0 AND 18),
    spot_config_version BIGINT NOT NULL CHECK (spot_config_version > 0),
    ui_multiplier_e18 TEXT NOT NULL CHECK (ui_multiplier_e18 ~ '^[1-9][0-9]{0,38}$'),
    max_price_deviation_bps INTEGER NOT NULL CHECK (max_price_deviation_bps BETWEEN 1 AND 500),
    max_spot_slippage_bps INTEGER NOT NULL CHECK (max_spot_slippage_bps BETWEEN 1 AND 1000),
    max_unwind_price_deviation_bps INTEGER NOT NULL
        CHECK (max_unwind_price_deviation_bps BETWEEN 1 AND 5000),
    review_record_sha256 TEXT NOT NULL CHECK (review_record_sha256 ~ '^[0-9a-f]{64}$'),
    valid_from TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (valid_until > valid_from)
);

CREATE TABLE execution_market_quotes (
    id BIGSERIAL PRIMARY KEY,
    source TEXT NOT NULL CHECK (source IN ('lighter-auth', 'robinhood-chain')),
    source_session TEXT NOT NULL,
    source_event_id TEXT NOT NULL,
    source_sequence BIGINT NOT NULL CHECK (source_sequence >= 0),
    market_manifest TEXT NOT NULL REFERENCES execution_market_configs(manifest_id),
    quote_block_hash TEXT NOT NULL CHECK (quote_block_hash ~ '^0x[0-9a-f]{64}$'),
    mark_price BIGINT NOT NULL CHECK (mark_price BETWEEN 1 AND 4294967295),
    payload_sha256 TEXT NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    publisher_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, source_session, source_event_id),
    UNIQUE (market_manifest, quote_block_hash, received_at),
    CHECK (expires_at > received_at)
);

CREATE INDEX execution_market_quotes_lookup
    ON execution_market_quotes (market_manifest, quote_block_hash, received_at, expires_at);

CREATE TRIGGER execution_market_configs_append_only
    BEFORE UPDATE OR DELETE ON execution_market_configs
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();

CREATE TRIGGER execution_market_quotes_append_only
    BEFORE UPDATE OR DELETE ON execution_market_quotes
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();
