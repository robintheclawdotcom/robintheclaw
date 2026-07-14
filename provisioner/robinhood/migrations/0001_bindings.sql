CREATE TABLE IF NOT EXISTS robinhood_execution_bindings (
    execution_account_id UUID PRIMARY KEY,
    owner_address TEXT NOT NULL UNIQUE CHECK (owner_address ~ '^0x[0-9a-f]{40}$'),
    kms_key_id TEXT NOT NULL UNIQUE,
    signer_address TEXT NOT NULL UNIQUE CHECK (signer_address ~ '^0x[0-9a-f]{40}$'),
    key_version BIGINT NOT NULL DEFAULT 1 CHECK (key_version > 0),
    factory_address TEXT NOT NULL CHECK (factory_address ~ '^0x[0-9a-f]{40}$'),
    registry_address TEXT NOT NULL CHECK (registry_address ~ '^0x[0-9a-f]{40}$'),
    policy_digest TEXT NOT NULL CHECK (policy_digest ~ '^0x[0-9a-f]{64}$'),
    factory_code_hash TEXT NOT NULL CHECK (factory_code_hash ~ '^0x[0-9a-f]{64}$'),
    registry_code_hash TEXT NOT NULL CHECK (registry_code_hash ~ '^0x[0-9a-f]{64}$'),
    vault_code_hash TEXT NOT NULL CHECK (vault_code_hash ~ '^0x[0-9a-f]{64}$'),
    risk_manager_code_hash TEXT NOT NULL CHECK (risk_manager_code_hash ~ '^0x[0-9a-f]{64}$'),
    spot_adapter_code_hash TEXT NOT NULL CHECK (spot_adapter_code_hash ~ '^0x[0-9a-f]{64}$'),
    vault_address TEXT NOT NULL UNIQUE CHECK (vault_address ~ '^0x[0-9a-f]{40}$'),
    risk_manager_address TEXT NOT NULL UNIQUE CHECK (risk_manager_address ~ '^0x[0-9a-f]{40}$'),
    spot_adapter_address TEXT NOT NULL UNIQUE CHECK (spot_adapter_address ~ '^0x[0-9a-f]{40}$'),
    deployment_tx_hash TEXT CHECK (deployment_tx_hash IS NULL OR deployment_tx_hash ~ '^0x[0-9a-f]{64}$'),
    deployment_block BIGINT CHECK (deployment_block IS NULL OR deployment_block >= 0),
    authorization_tx_hash TEXT CHECK (authorization_tx_hash IS NULL OR authorization_tx_hash ~ '^0x[0-9a-f]{64}$'),
    authorization_block BIGINT CHECK (authorization_block IS NULL OR authorization_block >= 0),
    status TEXT NOT NULL CHECK (status IN (
        'awaiting_deployment', 'confirming', 'active', 'rotation_pending', 'blocked'
    )),
    blocked_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE robinhood_execution_bindings
    ADD COLUMN IF NOT EXISTS authorization_tx_hash TEXT CHECK (
        authorization_tx_hash IS NULL OR authorization_tx_hash ~ '^0x[0-9a-f]{64}$'
    ),
    ADD COLUMN IF NOT EXISTS authorization_block BIGINT CHECK (
        authorization_block IS NULL OR authorization_block >= 0
    );

CREATE TABLE IF NOT EXISTS robinhood_provisioner_auth_nonces (
    caller_id TEXT NOT NULL,
    nonce TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (caller_id, nonce)
);

CREATE INDEX IF NOT EXISTS robinhood_provisioner_auth_nonce_expiry
    ON robinhood_provisioner_auth_nonces (expires_at);

CREATE TABLE IF NOT EXISTS robinhood_provisioner_audit (
    sequence BIGSERIAL PRIMARY KEY,
    execution_account_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    evidence JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
