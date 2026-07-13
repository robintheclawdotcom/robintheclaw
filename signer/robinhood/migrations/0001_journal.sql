CREATE TABLE robinhood_signer_deployments (
    deployment_id TEXT PRIMARY KEY CHECK (deployment_id ~ '^[0-9a-f]{64}$'),
    manifest JSONB NOT NULL,
    chain_id NUMERIC(78, 0) NOT NULL,
    signer_address TEXT NOT NULL,
    vault_address TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE robinhood_signer_nonces (
    chain_id NUMERIC(78, 0) NOT NULL,
    signer_address TEXT NOT NULL,
    next_nonce BIGINT NOT NULL CHECK (next_nonce >= 0),
    version BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, signer_address)
);

CREATE TABLE robinhood_signer_transactions (
    deployment_id TEXT NOT NULL REFERENCES robinhood_signer_deployments(deployment_id),
    request_id TEXT NOT NULL,
    intent_id TEXT NOT NULL,
    payload_sha256 TEXT NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    payload JSONB NOT NULL,
    nonce BIGINT NOT NULL CHECK (nonce >= 0),
    tx_hash TEXT NOT NULL CHECK (tx_hash ~ '^0x[0-9a-f]{64}$'),
    signed_transaction BYTEA NOT NULL,
    max_fee_per_gas NUMERIC(78, 0) NOT NULL,
    max_priority_fee_per_gas NUMERIC(78, 0) NOT NULL,
    gas_limit BIGINT NOT NULL CHECK (gas_limit > 0),
    status TEXT NOT NULL CHECK (status IN (
        'signed', 'submitted', 'soft_confirmed', 'l1_posted', 'ethereum_final',
        'reverted', 'ambiguous', 'replaced', 'superseded', 'quarantined'
    )),
    block_number BIGINT,
    block_hash TEXT,
    replaces_request_id TEXT,
    replaced_by_request_id TEXT,
    replacement_depth SMALLINT NOT NULL DEFAULT 0 CHECK (replacement_depth >= 0),
    family_created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    primary_verified_block BIGINT NOT NULL,
    primary_verified_hash TEXT NOT NULL CHECK (primary_verified_hash ~ '^0x[0-9a-f]{64}$'),
    secondary_verified_block BIGINT NOT NULL,
    secondary_verified_hash TEXT NOT NULL CHECK (secondary_verified_hash ~ '^0x[0-9a-f]{64}$'),
    reconcile_attempts INTEGER NOT NULL DEFAULT 0 CHECK (reconcile_attempts >= 0),
    next_reconcile_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_checked_at TIMESTAMPTZ,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (deployment_id, request_id),
    UNIQUE (deployment_id, tx_hash),
    FOREIGN KEY (deployment_id, replaces_request_id)
        REFERENCES robinhood_signer_transactions(deployment_id, request_id),
    FOREIGN KEY (deployment_id, replaced_by_request_id)
        REFERENCES robinhood_signer_transactions(deployment_id, request_id)
);

CREATE UNIQUE INDEX robinhood_signer_root_intent
    ON robinhood_signer_transactions (deployment_id, intent_id)
    WHERE replaces_request_id IS NULL;

CREATE INDEX robinhood_signer_pending_status
    ON robinhood_signer_transactions (deployment_id, next_reconcile_at, updated_at)
    WHERE status IN (
        'signed', 'submitted', 'soft_confirmed', 'l1_posted', 'ambiguous', 'replaced'
    );

CREATE TABLE robinhood_signer_auth_nonces (
    deployment_id TEXT NOT NULL REFERENCES robinhood_signer_deployments(deployment_id),
    nonce TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (deployment_id, nonce)
);

CREATE INDEX robinhood_signer_auth_nonce_expiry
    ON robinhood_signer_auth_nonces (expires_at);
