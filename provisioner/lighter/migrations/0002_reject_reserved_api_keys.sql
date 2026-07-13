DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'lighter_credentials_api_key_index_check'
          AND conrelid = 'lighter_credentials'::regclass
    ) THEN
        ALTER TABLE lighter_credentials
            DROP CONSTRAINT lighter_credentials_api_key_index_check;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'lighter_credentials_api_key_index_v2_check'
          AND conrelid = 'lighter_credentials'::regclass
    ) THEN
        ALTER TABLE lighter_credentials
            ADD CONSTRAINT lighter_credentials_api_key_index_v2_check
            CHECK (api_key_index BETWEEN 4 AND 254);
    END IF;

    IF EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'lighter_credential_bindings_api_key_index_check'
          AND conrelid = 'lighter_credential_bindings'::regclass
    ) THEN
        ALTER TABLE lighter_credential_bindings
            DROP CONSTRAINT lighter_credential_bindings_api_key_index_check;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'lighter_credential_bindings_api_key_index_v2_check'
          AND conrelid = 'lighter_credential_bindings'::regclass
    ) THEN
        ALTER TABLE lighter_credential_bindings
            ADD CONSTRAINT lighter_credential_bindings_api_key_index_v2_check
            CHECK (api_key_index BETWEEN 4 AND 254);
    END IF;
END;
$$;
