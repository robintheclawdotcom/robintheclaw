CREATE TABLE execution_promotion_evidence (
    id BIGSERIAL PRIMARY KEY,
    strategy_version TEXT NOT NULL,
    evidence JSONB NOT NULL,
    evidence_sha256 TEXT NOT NULL CHECK (evidence_sha256 ~ '^[0-9a-f]{64}$'),
    approved_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (strategy_version, evidence_sha256)
);

CREATE TABLE execution_intents (
    id TEXT PRIMARY KEY,
    strategy_version TEXT NOT NULL,
    symbol TEXT NOT NULL,
    direction TEXT NOT NULL CHECK (direction = 'long_spot_short_perp'),
    payload JSONB NOT NULL,
    saga JSONB NOT NULL,
    saga_version BIGINT NOT NULL DEFAULT 0,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX execution_one_active_episode
    ON execution_intents (strategy_version, symbol, direction)
    WHERE active;

CREATE TABLE execution_events (
    id BIGSERIAL PRIMARY KEY,
    intent_id TEXT NOT NULL REFERENCES execution_intents(id),
    saga_version BIGINT NOT NULL,
    event JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (intent_id, saga_version)
);

CREATE TABLE execution_venue_nonces (
    venue TEXT NOT NULL,
    account_index BIGINT NOT NULL,
    api_key_index SMALLINT NOT NULL,
    next_nonce BIGINT NOT NULL CHECK (next_nonce >= 0),
    version BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (venue, account_index, api_key_index)
);

CREATE TABLE execution_signer_requests (
    request_id TEXT PRIMARY KEY,
    intent_id TEXT NOT NULL REFERENCES execution_intents(id),
    signer TEXT NOT NULL,
    request_sha256 TEXT NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    response_sha256 TEXT CHECK (response_sha256 ~ '^[0-9a-f]{64}$'),
    status TEXT NOT NULL CHECK (status IN ('created', 'accepted', 'rejected', 'ambiguous')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE TABLE execution_incidents (
    id BIGSERIAL PRIMARY KEY,
    intent_id TEXT REFERENCES execution_intents(id),
    severity TEXT NOT NULL CHECK (severity IN ('warning', 'critical')),
    kind TEXT NOT NULL,
    details JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);

CREATE OR REPLACE FUNCTION execution_reject_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME;
END;
$$;

CREATE TRIGGER execution_events_append_only
    BEFORE UPDATE OR DELETE ON execution_events
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();

CREATE TRIGGER execution_evidence_append_only
    BEFORE UPDATE OR DELETE ON execution_promotion_evidence
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();

