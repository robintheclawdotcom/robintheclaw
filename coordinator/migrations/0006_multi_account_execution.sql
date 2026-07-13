CREATE TABLE execution_accounts (
    execution_account_id TEXT PRIMARY KEY
        CHECK (execution_account_id ~ '^[a-z0-9][a-z0-9-]{7,63}$'),
    agent_id TEXT NOT NULL UNIQUE CHECK (agent_id ~ '^[a-z0-9][a-z0-9-]{7,63}$'),
    strategy_version TEXT NOT NULL,
    risk_version TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('provisioning', 'active', 'blocked', 'closed')),
    lighter_account_index BIGINT CHECK (lighter_account_index > 0),
    lighter_api_key_index SMALLINT CHECK (lighter_api_key_index BETWEEN 4 AND 254),
    robinhood_vault TEXT CHECK (robinhood_vault ~ '^0x[0-9a-f]{40}$'),
    robinhood_signer TEXT CHECK (robinhood_signer ~ '^0x[0-9a-f]{40}$'),
    binding_sha256 TEXT CHECK (binding_sha256 ~ '^[0-9a-f]{64}$'),
    binding_version BIGINT NOT NULL DEFAULT 1 CHECK (binding_version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((lighter_account_index IS NULL) = (lighter_api_key_index IS NULL)),
    CHECK ((robinhood_vault IS NULL) = (robinhood_signer IS NULL)),
    CHECK (robinhood_vault IS NULL OR robinhood_vault <> robinhood_signer),
    CHECK (
        status <> 'active' OR
        (lighter_account_index IS NOT NULL AND robinhood_vault IS NOT NULL
         AND binding_sha256 IS NOT NULL)
    ),
    UNIQUE (lighter_account_index, lighter_api_key_index),
    UNIQUE (robinhood_vault),
    UNIQUE (robinhood_signer)
);

ALTER TABLE execution_api_nonces
    DROP CONSTRAINT execution_api_nonces_scope_check;

ALTER TABLE execution_api_nonces
    ADD CONSTRAINT execution_api_nonces_scope_check
        CHECK (scope IN (
            'intent', 'exit', 'recovery', 'venue_event', 'market_quote', 'account_snapshot'
        ));

INSERT INTO execution_accounts (
    execution_account_id, agent_id, strategy_version, risk_version, status
) VALUES (
    'singleton-mainnet-canary', 'singleton-mainnet-canary',
    'legacy-singleton', 'legacy-singleton', 'blocked'
);

CREATE TABLE execution_account_control (
    execution_account_id TEXT PRIMARY KEY REFERENCES execution_accounts(execution_account_id),
    mode TEXT NOT NULL CHECK (mode IN ('ACTIVE', 'REDUCE_ONLY', 'HALTED')),
    reason TEXT NOT NULL,
    version BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO execution_account_control (execution_account_id, mode, reason)
VALUES ('singleton-mainnet-canary', 'HALTED', 'migration requires explicit account binding');

CREATE TABLE execution_account_readiness (
    execution_account_id TEXT PRIMARY KEY REFERENCES execution_accounts(execution_account_id),
    venue_approved BOOLEAN NOT NULL DEFAULT FALSE,
    oracle_healthy BOOLEAN NOT NULL DEFAULT FALSE,
    sequencer_healthy BOOLEAN NOT NULL DEFAULT FALSE,
    reconciliation_ready BOOLEAN NOT NULL DEFAULT FALSE,
    exit_authority_ready BOOLEAN NOT NULL DEFAULT FALSE,
    alerting_ready BOOLEAN NOT NULL DEFAULT FALSE,
    safe_rotation_ready BOOLEAN NOT NULL DEFAULT FALSE,
    version BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO execution_account_readiness (execution_account_id)
VALUES ('singleton-mainnet-canary');

CREATE TABLE execution_account_snapshots (
    id BIGSERIAL PRIMARY KEY,
    execution_account_id TEXT NOT NULL REFERENCES execution_accounts(execution_account_id),
    source TEXT NOT NULL CHECK (source IN ('lighter-auth', 'robinhood-chain')),
    source_session TEXT NOT NULL,
    source_sequence BIGINT NOT NULL CHECK (source_sequence >= 0),
    payload JSONB NOT NULL,
    payload_sha256 TEXT NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    observed_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (expires_at > received_at),
    UNIQUE (execution_account_id, source, source_session, source_sequence)
);

CREATE INDEX execution_account_snapshots_current
    ON execution_account_snapshots (execution_account_id, source, expires_at DESC, id DESC);

CREATE TRIGGER execution_account_snapshots_append_only
    BEFORE UPDATE OR DELETE ON execution_account_snapshots
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();

CREATE TABLE execution_account_daily_turnover (
    execution_account_id TEXT NOT NULL REFERENCES execution_accounts(execution_account_id),
    trading_day DATE NOT NULL,
    entry_gross_micros BIGINT NOT NULL DEFAULT 0 CHECK (entry_gross_micros >= 0),
    version BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (execution_account_id, trading_day)
);

ALTER TABLE execution_intents
    ADD COLUMN execution_account_id TEXT REFERENCES execution_accounts(execution_account_id),
    ADD COLUMN agent_id TEXT,
    ADD COLUMN source_evaluation_id TEXT,
    ADD COLUMN risk_version TEXT;

UPDATE execution_intents
SET execution_account_id = 'singleton-mainnet-canary',
    agent_id = 'singleton-mainnet-canary',
    source_evaluation_id = id,
    risk_version = 'legacy-singleton',
    active = FALSE;

ALTER TABLE execution_intents
    ALTER COLUMN execution_account_id SET NOT NULL,
    ALTER COLUMN agent_id SET NOT NULL,
    ALTER COLUMN source_evaluation_id SET NOT NULL,
    ALTER COLUMN risk_version SET NOT NULL,
    ADD CONSTRAINT execution_intents_account_identity
        UNIQUE (id, execution_account_id);

DROP INDEX execution_one_active_episode;

CREATE UNIQUE INDEX execution_one_active_episode_per_account
    ON execution_intents (execution_account_id)
    WHERE active;

ALTER TABLE execution_identifiers
    ADD COLUMN execution_account_id TEXT REFERENCES execution_accounts(execution_account_id);

UPDATE execution_identifiers identifier
SET execution_account_id = intent.execution_account_id
FROM execution_intents intent
WHERE intent.id = identifier.intent_id;

ALTER TABLE execution_identifiers
    ALTER COLUMN execution_account_id SET NOT NULL,
    DROP CONSTRAINT execution_identifiers_pkey,
    ADD PRIMARY KEY (execution_account_id, namespace, value);

ALTER TABLE execution_venue_events
    ADD COLUMN execution_account_id TEXT REFERENCES execution_accounts(execution_account_id);

UPDATE execution_venue_events event
SET execution_account_id = intent.execution_account_id
FROM execution_intents intent
WHERE intent.id = event.intent_id;

ALTER TABLE execution_venue_events
    ALTER COLUMN execution_account_id SET NOT NULL,
    ADD CONSTRAINT execution_venue_events_intent_account
        FOREIGN KEY (intent_id, execution_account_id)
        REFERENCES execution_intents(id, execution_account_id),
    DROP CONSTRAINT execution_venue_events_canonical_identity,
    ADD CONSTRAINT execution_venue_events_canonical_identity
        UNIQUE (execution_account_id, source, source_session, source_event_id);

ALTER TABLE execution_venue_source_sessions
    ADD COLUMN execution_account_id TEXT REFERENCES execution_accounts(execution_account_id);

UPDATE execution_venue_source_sessions
SET execution_account_id = 'singleton-mainnet-canary';

ALTER TABLE execution_venue_source_sessions
    ALTER COLUMN execution_account_id SET NOT NULL,
    DROP CONSTRAINT execution_venue_source_sessions_pkey,
    ADD PRIMARY KEY (execution_account_id, source, source_session);

ALTER TABLE execution_lighter_nonce_reservations
    ADD COLUMN execution_account_id TEXT REFERENCES execution_accounts(execution_account_id);

UPDATE execution_lighter_nonce_reservations reservation
SET execution_account_id = intent.execution_account_id
FROM execution_actions action
JOIN execution_intents intent ON intent.id = action.intent_id
WHERE action.id = reservation.action_id;

ALTER TABLE execution_lighter_nonce_reservations
    ALTER COLUMN execution_account_id SET NOT NULL,
    DROP CONSTRAINT execution_lighter_nonce_reser_account_index_api_key_index_n_key,
    ADD CONSTRAINT execution_lighter_nonce_reservations_account_nonce
        UNIQUE (execution_account_id, account_index, api_key_index, nonce);

ALTER TABLE execution_venue_nonces
    ADD COLUMN execution_account_id TEXT REFERENCES execution_accounts(execution_account_id);

UPDATE execution_venue_nonces
SET execution_account_id = 'singleton-mainnet-canary';

ALTER TABLE execution_venue_nonces
    ALTER COLUMN execution_account_id SET NOT NULL,
    DROP CONSTRAINT execution_venue_nonces_pkey,
    ADD PRIMARY KEY (execution_account_id, venue, account_index, api_key_index);

ALTER TABLE execution_signer_requests
    ADD COLUMN execution_account_id TEXT REFERENCES execution_accounts(execution_account_id);

UPDATE execution_signer_requests request
SET execution_account_id = intent.execution_account_id
FROM execution_intents intent
WHERE intent.id = request.intent_id;

ALTER TABLE execution_signer_requests
    ALTER COLUMN execution_account_id SET NOT NULL;

ALTER TABLE execution_incidents
    ADD COLUMN execution_account_id TEXT REFERENCES execution_accounts(execution_account_id);

UPDATE execution_incidents incident
SET execution_account_id = intent.execution_account_id
FROM execution_intents intent
WHERE intent.id = incident.intent_id;

UPDATE execution_control
SET mode = 'HALTED', reason = 'multi-account migration requires explicit readiness',
    version = version + 1, updated_at = now()
WHERE singleton;
