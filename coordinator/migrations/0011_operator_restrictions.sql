CREATE TABLE execution_operator_restriction_events (
    request_id TEXT PRIMARY KEY
        CHECK (request_id ~ '^[a-z0-9][a-z0-9-]{7,63}$'),
    request_sha256 TEXT NOT NULL UNIQUE CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    scope TEXT NOT NULL CHECK (scope IN ('global', 'strategy', 'account')),
    strategy_version TEXT REFERENCES execution_strategy_control(strategy_version),
    execution_account_id TEXT REFERENCES execution_accounts(execution_account_id),
    from_mode TEXT NOT NULL CHECK (from_mode IN ('ACTIVE', 'REDUCE_ONLY')),
    to_mode TEXT NOT NULL CHECK (to_mode IN ('REDUCE_ONLY', 'HALTED')),
    expected_version BIGINT NOT NULL CHECK (expected_version >= 0),
    resulting_version BIGINT NOT NULL CHECK (resulting_version = expected_version + 1),
    reason TEXT NOT NULL CHECK (length(btrim(reason)) BETWEEN 8 AND 512),
    evidence_sha256 TEXT NOT NULL CHECK (evidence_sha256 ~ '^[0-9a-f]{64}$'),
    operator_id TEXT NOT NULL CHECK (operator_id ~ '^[a-z0-9][a-z0-9-]{2,63}$'),
    signer_key_id TEXT NOT NULL CHECK (signer_key_id ~ '^[0-9a-f]{64}$'),
    request_payload JSONB NOT NULL,
    signer_public_key BYTEA NOT NULL CHECK (octet_length(signer_public_key) = 32),
    signature BYTEA NOT NULL CHECK (octet_length(signature) = 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (scope = 'global' AND strategy_version IS NULL AND execution_account_id IS NULL) OR
        (scope = 'strategy' AND strategy_version IS NOT NULL AND execution_account_id IS NULL) OR
        (scope = 'account' AND strategy_version IS NOT NULL AND execution_account_id IS NOT NULL)
    ),
    CHECK (
        (from_mode = 'ACTIVE' AND to_mode IN ('REDUCE_ONLY', 'HALTED')) OR
        (from_mode = 'REDUCE_ONLY' AND to_mode = 'HALTED')
    )
);

CREATE INDEX execution_operator_restriction_events_target
    ON execution_operator_restriction_events
        (scope, strategy_version, execution_account_id, resulting_version);

CREATE TRIGGER execution_operator_restriction_events_append_only
    BEFORE UPDATE OR DELETE ON execution_operator_restriction_events
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();
