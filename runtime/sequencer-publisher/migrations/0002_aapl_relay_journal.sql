CREATE TABLE IF NOT EXISTS aapl_relay_state (
    publisher_id text PRIMARY KEY,
    source_chain_id bigint NOT NULL,
    source_feed text NOT NULL,
    source_code_hash text NOT NULL,
    target_chain_id bigint NOT NULL,
    target_feed text NOT NULL,
    target_code_hash text NOT NULL,
    signer_address text NOT NULL,
    last_source_round numeric(24,0) NOT NULL DEFAULT 0,
    last_source_answer numeric(58,0) NOT NULL DEFAULT 0,
    last_source_updated_at bigint NOT NULL DEFAULT 0,
    last_answered_in_round numeric(24,0) NOT NULL DEFAULT 0,
    last_source_block bigint NOT NULL DEFAULT 0,
    last_source_block_hash text NOT NULL DEFAULT '',
    last_sequence bigint NOT NULL DEFAULT 0,
    last_nonce bigint,
    quarantined_reason text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS aapl_relay_transactions (
    publisher_id text NOT NULL REFERENCES aapl_relay_state(publisher_id),
    sequence bigint NOT NULL,
    nonce bigint NOT NULL,
    tx_hash text NOT NULL,
    raw_transaction bytea NOT NULL,
    source_round numeric(24,0) NOT NULL,
    source_answer numeric(58,0) NOT NULL,
    source_updated_at bigint NOT NULL,
    answered_in_round numeric(24,0) NOT NULL,
    status text NOT NULL CHECK (status IN ('signed', 'submitted', 'confirmed', 'failed', 'quarantined')),
    error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (publisher_id, sequence),
    UNIQUE (publisher_id, nonce),
    UNIQUE (tx_hash)
);

CREATE UNIQUE INDEX IF NOT EXISTS aapl_relay_one_pending
    ON aapl_relay_transactions (publisher_id)
    WHERE status IN ('signed', 'submitted');
