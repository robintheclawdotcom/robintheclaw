ALTER TABLE execution_venue_events
    DROP CONSTRAINT execution_venue_events_source_source_event_id_key;

ALTER TABLE execution_venue_events
    ADD CONSTRAINT execution_venue_events_canonical_identity
    UNIQUE (source, source_session, source_event_id);

CREATE TABLE execution_venue_source_sessions (
    source TEXT NOT NULL,
    source_session TEXT NOT NULL,
    first_sequence BIGINT NOT NULL CHECK (first_sequence >= 0),
    last_sequence BIGINT NOT NULL CHECK (last_sequence >= first_sequence),
    first_received_at TIMESTAMPTZ NOT NULL,
    last_received_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (source, source_session)
);

CREATE TABLE execution_venue_event_routes (
    venue_event_id BIGINT PRIMARY KEY REFERENCES execution_venue_events(id),
    action_id TEXT REFERENCES execution_actions(id),
    disposition TEXT NOT NULL CHECK (disposition IN ('matched', 'quarantined')),
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (disposition = 'matched' AND action_id IS NOT NULL) OR
        (disposition = 'quarantined' AND action_id IS NULL)
    )
);

CREATE INDEX execution_venue_event_routes_action
    ON execution_venue_event_routes (action_id, venue_event_id)
    WHERE disposition = 'matched';

CREATE TABLE execution_lighter_nonce_reservations (
    action_id TEXT PRIMARY KEY REFERENCES execution_actions(id),
    account_index BIGINT NOT NULL CHECK (account_index > 0),
    api_key_index SMALLINT NOT NULL CHECK (api_key_index BETWEEN 2 AND 254),
    nonce BIGINT NOT NULL CHECK (nonce >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (account_index, api_key_index, nonce)
);

CREATE TRIGGER execution_venue_event_routes_append_only
    BEFORE UPDATE OR DELETE ON execution_venue_event_routes
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();

CREATE TRIGGER execution_lighter_nonce_reservations_append_only
    BEFORE UPDATE OR DELETE ON execution_lighter_nonce_reservations
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();
