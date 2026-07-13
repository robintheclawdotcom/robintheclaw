DROP INDEX execution_one_active_episode;

CREATE UNIQUE INDEX execution_one_active_episode
    ON execution_intents ((active))
    WHERE active;

CREATE TABLE execution_control (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    mode TEXT NOT NULL CHECK (mode IN ('ACTIVE', 'REDUCE_ONLY', 'HALTED')),
    reason TEXT NOT NULL,
    version BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO execution_control (mode, reason)
VALUES ('HALTED', 'initial deployment');

CREATE TABLE execution_identifiers (
    namespace TEXT NOT NULL CHECK (namespace IN ('spot_intent', 'lighter_client_order')),
    value TEXT NOT NULL,
    intent_id TEXT NOT NULL REFERENCES execution_intents(id),
    PRIMARY KEY (namespace, value)
);

CREATE TABLE execution_unwind_cursors (
    intent_id TEXT PRIMARY KEY REFERENCES execution_intents(id),
    next_attempt SMALLINT NOT NULL DEFAULT 0 CHECK (next_attempt BETWEEN 0 AND 8),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE SEQUENCE execution_operator_order_index_seq
    AS BIGINT
    MINVALUE 1099511627776
    MAXVALUE 281474976710655
    NO CYCLE;

CREATE TABLE execution_actions (
    id TEXT PRIMARY KEY CHECK (id ~ '^[0-9a-f]{32}$'),
    intent_id TEXT NOT NULL REFERENCES execution_intents(id),
    kind TEXT NOT NULL CHECK (kind IN (
        'submit_perp', 'reconcile_perp', 'submit_spot', 'reconcile_spot',
        'unwind_perp', 'reconcile_unwind', 'unwind_spot', 'reconcile_unwind_spot'
    )),
    action_key TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    result JSONB,
    status TEXT NOT NULL CHECK (status IN (
        'pending', 'leased', 'succeeded', 'rejected', 'ambiguous', 'failed_safe'
    )),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_owner TEXT,
    lease_token TEXT CHECK (lease_token IS NULL OR lease_token ~ '^[0-9a-f]{32}$'),
    lease_expires_at TIMESTAMPTZ,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    UNIQUE (intent_id, action_key),
    CHECK (
        (status = 'leased' AND lease_owner IS NOT NULL AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL) OR
        (status <> 'leased' AND lease_owner IS NULL AND lease_token IS NULL AND lease_expires_at IS NULL)
    )
);

CREATE TABLE execution_api_nonces (
    scope TEXT NOT NULL CHECK (scope IN ('intent', 'venue_event')),
    nonce TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (scope, nonce)
);

CREATE INDEX execution_api_nonce_expiry
    ON execution_api_nonces (expires_at);

CREATE INDEX execution_actions_ready
    ON execution_actions (available_at, created_at)
    WHERE status IN ('pending', 'leased');

CREATE TABLE execution_action_events (
    id BIGSERIAL PRIMARY KEY,
    action_id TEXT NOT NULL REFERENCES execution_actions(id),
    intent_id TEXT NOT NULL REFERENCES execution_intents(id),
    status TEXT NOT NULL,
    details JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE execution_venue_events (
    id BIGSERIAL PRIMARY KEY,
    source TEXT NOT NULL,
    source_session TEXT NOT NULL,
    source_event_id TEXT NOT NULL,
    source_sequence BIGINT NOT NULL CHECK (source_sequence >= 0),
    intent_id TEXT NOT NULL REFERENCES execution_intents(id),
    kind TEXT NOT NULL CHECK (kind IN (
        'perp_accepted', 'perp_partial', 'perp_filled', 'perp_rejected',
        'spot_confirmed', 'spot_rejected', 'unwind_accepted', 'unwind_partial',
        'unwind_filled', 'unwind_rejected',
        'spot_unwind_confirmed', 'spot_unwind_rejected'
    )),
    payload JSONB NOT NULL,
    payload_sha256 TEXT NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    publisher_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, source_event_id)
);

CREATE INDEX execution_venue_events_intent
    ON execution_venue_events (intent_id, source_sequence, id);

CREATE TABLE execution_applied_venue_events (
    venue_event_id BIGINT PRIMARY KEY REFERENCES execution_venue_events(id),
    action_id TEXT NOT NULL REFERENCES execution_actions(id),
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER execution_action_events_append_only
    BEFORE UPDATE OR DELETE ON execution_action_events
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();

CREATE TRIGGER execution_venue_events_append_only
    BEFORE UPDATE OR DELETE ON execution_venue_events
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();

CREATE TRIGGER execution_applied_venue_events_append_only
    BEFORE UPDATE OR DELETE ON execution_applied_venue_events
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();
