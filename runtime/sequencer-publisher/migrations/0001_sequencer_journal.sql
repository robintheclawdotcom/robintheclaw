CREATE TABLE IF NOT EXISTS sequencer_publisher_state (
    publisher_id text PRIMARY KEY,
    chain_id bigint NOT NULL CHECK (chain_id = 4663),
    feed_address text NOT NULL,
    signer_address text NOT NULL,
    last_latest_number bigint NOT NULL DEFAULT 0 CHECK (last_latest_number >= 0),
    last_latest_hash text NOT NULL DEFAULT '',
    last_finalized_number bigint NOT NULL DEFAULT 0 CHECK (last_finalized_number >= 0),
    last_finalized_hash text NOT NULL DEFAULT '',
    observed_healthy boolean NOT NULL DEFAULT false,
    continuous_started_at bigint NOT NULL DEFAULT 0 CHECK (continuous_started_at >= 0),
    observed_at timestamptz,
    last_sequence bigint NOT NULL DEFAULT 0 CHECK (last_sequence >= 0),
    last_nonce bigint CHECK (last_nonce >= 0),
    quarantined_reason text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (chain_id, signer_address)
);

CREATE TABLE IF NOT EXISTS sequencer_publisher_transactions (
    publisher_id text NOT NULL REFERENCES sequencer_publisher_state(publisher_id),
    sequence bigint NOT NULL CHECK (sequence > 0),
    nonce bigint NOT NULL CHECK (nonce >= 0),
    tx_hash text NOT NULL UNIQUE,
    raw_transaction bytea NOT NULL,
    healthy boolean NOT NULL,
    started_at bigint NOT NULL CHECK (started_at > 0),
    status text NOT NULL CHECK (status IN ('signed', 'submitted', 'confirmed', 'reverted', 'quarantined')),
    error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    submitted_at timestamptz,
    confirmed_at timestamptz,
    PRIMARY KEY (publisher_id, sequence)
);

CREATE UNIQUE INDEX IF NOT EXISTS sequencer_publisher_one_pending
    ON sequencer_publisher_transactions (publisher_id)
    WHERE status IN ('signed', 'submitted');
