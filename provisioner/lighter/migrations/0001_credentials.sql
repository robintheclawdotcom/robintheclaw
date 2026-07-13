CREATE TABLE IF NOT EXISTS lighter_credentials (
    id uuid PRIMARY KEY,
    execution_account_id uuid NOT NULL,
    owner_address text NOT NULL CHECK (owner_address ~ '^0x[0-9a-f]{40}$'),
    account_index bigint NOT NULL CHECK (account_index > 0),
    api_key_index smallint NOT NULL CHECK (api_key_index BETWEEN 4 AND 254),
    version bigint NOT NULL CHECK (version > 0),
    public_key text,
    encrypted_data_key bytea,
    cipher_nonce bytea,
    ciphertext bytea,
    aad_sha256 bytea,
    kms_key_id text,
    change_nonce bigint,
    expires_at_ms bigint,
    tx_type smallint,
    tx_hash text,
    tx_info jsonb,
    message_to_sign text,
    l1_signature text,
    status text NOT NULL CHECK (status IN (
        'generating', 'pending', 'verifying', 'linked', 'superseded', 'blocked'
    )),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (execution_account_id, version)
);

CREATE UNIQUE INDEX IF NOT EXISTS lighter_credentials_open_key
    ON lighter_credentials (execution_account_id, api_key_index)
    WHERE status IN ('generating', 'pending', 'verifying');

CREATE UNIQUE INDEX IF NOT EXISTS lighter_credentials_active_account
    ON lighter_credentials (execution_account_id)
    WHERE status = 'linked';

CREATE TABLE IF NOT EXISTS lighter_credential_bindings (
    execution_account_id uuid PRIMARY KEY,
    owner_address text NOT NULL CHECK (owner_address ~ '^0x[0-9a-f]{40}$'),
    account_index bigint NOT NULL CHECK (account_index > 0),
    api_key_index smallint NOT NULL CHECK (api_key_index BETWEEN 4 AND 254),
    status text NOT NULL CHECK (status IN ('pending', 'rotation_pending', 'verifying', 'linked', 'blocked')),
    active_credential_id uuid REFERENCES lighter_credentials(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_index)
);

CREATE TABLE IF NOT EXISTS lighter_credential_audit (
    id uuid PRIMARY KEY,
    execution_account_id uuid NOT NULL,
    credential_id uuid REFERENCES lighter_credentials(id) ON DELETE RESTRICT,
    event text NOT NULL,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION reject_lighter_credential_audit_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'lighter credential audit is append-only';
END;
$$;

DROP TRIGGER IF EXISTS lighter_credential_audit_append_only ON lighter_credential_audit;
CREATE TRIGGER lighter_credential_audit_append_only
    BEFORE UPDATE OR DELETE ON lighter_credential_audit
    FOR EACH ROW EXECUTE FUNCTION reject_lighter_credential_audit_mutation();

CREATE TABLE IF NOT EXISTS lighter_provisioner_request_nonces (
    caller text NOT NULL,
    nonce text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (caller, nonce)
);
