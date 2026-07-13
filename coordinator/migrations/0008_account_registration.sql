ALTER TABLE execution_api_nonces
    DROP CONSTRAINT execution_api_nonces_scope_check;

ALTER TABLE execution_api_nonces
    ADD CONSTRAINT execution_api_nonces_scope_check
        CHECK (scope IN (
            'intent', 'exit', 'recovery', 'venue_event', 'market_quote', 'account_snapshot',
            'account_command', 'account_registration'
        ));

ALTER TABLE execution_accounts
    ADD CONSTRAINT execution_accounts_owner_distinct
        CHECK (
            owner_address IS NULL OR
            (owner_address <> robinhood_vault AND owner_address <> robinhood_signer)
        );

CREATE UNIQUE INDEX execution_accounts_owner
    ON execution_accounts (owner_address)
    WHERE owner_address IS NOT NULL;

CREATE TABLE execution_account_registrations (
    execution_account_id TEXT PRIMARY KEY REFERENCES execution_accounts(execution_account_id),
    agent_id TEXT NOT NULL UNIQUE,
    strategy_version TEXT NOT NULL,
    risk_version TEXT NOT NULL,
    strategy_manifest_sha256 TEXT NOT NULL CHECK (strategy_manifest_sha256 ~ '^[0-9a-f]{64}$'),
    lighter_account_index BIGINT NOT NULL CHECK (lighter_account_index > 0),
    lighter_api_key_index SMALLINT NOT NULL CHECK (lighter_api_key_index BETWEEN 4 AND 254),
    robinhood_owner TEXT NOT NULL CHECK (robinhood_owner ~ '^0x[0-9a-f]{40}$'),
    robinhood_vault TEXT NOT NULL CHECK (robinhood_vault ~ '^0x[0-9a-f]{40}$'),
    robinhood_signer TEXT NOT NULL CHECK (robinhood_signer ~ '^0x[0-9a-f]{40}$'),
    binding_sha256 TEXT NOT NULL UNIQUE CHECK (binding_sha256 ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (robinhood_owner <> robinhood_vault),
    CHECK (robinhood_owner <> robinhood_signer),
    CHECK (robinhood_vault <> robinhood_signer),
    UNIQUE (lighter_account_index, lighter_api_key_index),
    UNIQUE (robinhood_owner),
    UNIQUE (robinhood_vault),
    UNIQUE (robinhood_signer)
);

CREATE TRIGGER execution_account_registrations_append_only
    BEFORE UPDATE OR DELETE ON execution_account_registrations
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();

CREATE TABLE execution_account_registration_addresses (
    address TEXT PRIMARY KEY CHECK (address ~ '^0x[0-9a-f]{40}$'),
    execution_account_id TEXT NOT NULL
        REFERENCES execution_account_registrations(execution_account_id),
    role TEXT NOT NULL CHECK (role IN ('owner', 'vault', 'signer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (execution_account_id, role)
);

CREATE TRIGGER execution_account_registration_addresses_append_only
    BEFORE UPDATE OR DELETE ON execution_account_registration_addresses
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();

CREATE OR REPLACE FUNCTION execution_protect_registered_account()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM execution_account_registrations registration
        WHERE registration.execution_account_id = OLD.execution_account_id
    ) AND (
        NEW.execution_account_id IS DISTINCT FROM OLD.execution_account_id OR
        NEW.agent_id IS DISTINCT FROM OLD.agent_id OR
        NEW.strategy_version IS DISTINCT FROM OLD.strategy_version OR
        NEW.risk_version IS DISTINCT FROM OLD.risk_version OR
        NEW.lighter_account_index IS DISTINCT FROM OLD.lighter_account_index OR
        NEW.lighter_api_key_index IS DISTINCT FROM OLD.lighter_api_key_index OR
        NEW.robinhood_vault IS DISTINCT FROM OLD.robinhood_vault OR
        NEW.robinhood_signer IS DISTINCT FROM OLD.robinhood_signer OR
        NEW.owner_address IS DISTINCT FROM OLD.owner_address OR
        NEW.strategy_manifest_sha256 IS DISTINCT FROM OLD.strategy_manifest_sha256 OR
        NEW.binding_sha256 IS DISTINCT FROM OLD.binding_sha256 OR
        NEW.binding_version IS DISTINCT FROM OLD.binding_version
    ) THEN
        RAISE EXCEPTION 'registered execution account binding is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_accounts_registered_binding_immutable
    BEFORE UPDATE ON execution_accounts
    FOR EACH ROW EXECUTE FUNCTION execution_protect_registered_account();
