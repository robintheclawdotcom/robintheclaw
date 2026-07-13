CREATE TABLE coordinator_account_registrations (
    execution_account_id uuid PRIMARY KEY REFERENCES execution_accounts(id) ON DELETE RESTRICT,
    agent_id uuid NOT NULL UNIQUE REFERENCES agents(id) ON DELETE RESTRICT,
    strategy_version text NOT NULL CHECK (strategy_version = 'basis-aapl-v1'),
    risk_version text NOT NULL CHECK (risk_version = 'basis-aapl-v1'),
    strategy_manifest_sha256 text NOT NULL CHECK (
        strategy_manifest_sha256 = '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'
    ),
    lighter_account_index bigint NOT NULL CHECK (lighter_account_index > 0),
    lighter_api_key_index smallint NOT NULL CHECK (lighter_api_key_index BETWEEN 2 AND 254),
    robinhood_owner text NOT NULL CHECK (robinhood_owner ~ '^0x[0-9a-f]{40}$'),
    robinhood_vault text NOT NULL CHECK (robinhood_vault ~ '^0x[0-9a-f]{40}$'),
    robinhood_signer text NOT NULL CHECK (robinhood_signer ~ '^0x[0-9a-f]{40}$'),
    binding_sha256 text NOT NULL UNIQUE CHECK (binding_sha256 ~ '^[0-9a-f]{64}$'),
    status text NOT NULL DEFAULT 'pending' CHECK (
        status IN ('pending', 'processing', 'registered', 'blocked')
    ),
    coordinator_account_status text,
    coordinator_control_mode text,
    last_error text,
    registered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (robinhood_owner <> robinhood_vault),
    CHECK (robinhood_owner <> robinhood_signer),
    CHECK (robinhood_vault <> robinhood_signer),
    UNIQUE (lighter_account_index, lighter_api_key_index),
    UNIQUE (robinhood_owner),
    UNIQUE (robinhood_vault),
    UNIQUE (robinhood_signer)
);

CREATE FUNCTION protect_coordinator_account_registration() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.execution_account_id IS DISTINCT FROM OLD.execution_account_id
       OR NEW.agent_id IS DISTINCT FROM OLD.agent_id
       OR NEW.strategy_version IS DISTINCT FROM OLD.strategy_version
       OR NEW.risk_version IS DISTINCT FROM OLD.risk_version
       OR NEW.strategy_manifest_sha256 IS DISTINCT FROM OLD.strategy_manifest_sha256
       OR NEW.lighter_account_index IS DISTINCT FROM OLD.lighter_account_index
       OR NEW.lighter_api_key_index IS DISTINCT FROM OLD.lighter_api_key_index
       OR NEW.robinhood_owner IS DISTINCT FROM OLD.robinhood_owner
       OR NEW.robinhood_vault IS DISTINCT FROM OLD.robinhood_vault
       OR NEW.robinhood_signer IS DISTINCT FROM OLD.robinhood_signer
       OR NEW.binding_sha256 IS DISTINCT FROM OLD.binding_sha256 THEN
        RAISE EXCEPTION 'coordinator account registration binding is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER coordinator_account_registration_immutable
    BEFORE UPDATE ON coordinator_account_registrations
    FOR EACH ROW EXECUTE FUNCTION protect_coordinator_account_registration();

CREATE TABLE coordinator_account_registration_outbox (
    execution_account_id uuid PRIMARY KEY
        REFERENCES coordinator_account_registrations(execution_account_id) ON DELETE RESTRICT,
    available_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    claimed_by text,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    delivered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX coordinator_account_registration_outbox_pending_idx
    ON coordinator_account_registration_outbox(available_at, execution_account_id)
    WHERE delivered_at IS NULL;
