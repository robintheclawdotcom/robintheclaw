CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE execution_market_review_records (
    review_record_sha256 TEXT PRIMARY KEY
        CHECK (review_record_sha256 ~ '^[0-9a-f]{64}$'),
    record JSONB NOT NULL CHECK (jsonb_typeof(record) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE execution_market_review_observations (
    id BIGSERIAL PRIMARY KEY,
    review_record_sha256 TEXT NOT NULL
        REFERENCES execution_market_review_records(review_record_sha256),
    source TEXT NOT NULL
        CHECK (source = 'https://mainnet.zklighter.elliot.ai/api/v1/orderBooks'),
    response_sha256 TEXT NOT NULL CHECK (response_sha256 ~ '^[0-9a-f]{64}$'),
    observed_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (review_record_sha256, response_sha256)
);

ALTER TABLE execution_market_configs
    ADD CONSTRAINT execution_market_configs_review_record_fk
    FOREIGN KEY (review_record_sha256)
    REFERENCES execution_market_review_records(review_record_sha256)
    NOT VALID;

ALTER TABLE execution_market_configs
    ADD CONSTRAINT execution_market_configs_no_overlap
    EXCLUDE USING gist (
        symbol WITH =,
        tstzrange(valid_from, valid_until, '[)') WITH &&
    );

CREATE TRIGGER execution_market_review_records_append_only
    BEFORE UPDATE OR DELETE ON execution_market_review_records
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();

CREATE TRIGGER execution_market_review_observations_append_only
    BEFORE UPDATE OR DELETE ON execution_market_review_observations
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();
