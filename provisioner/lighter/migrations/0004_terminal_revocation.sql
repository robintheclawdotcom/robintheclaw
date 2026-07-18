ALTER TABLE lighter_credentials
    ADD COLUMN IF NOT EXISTS purpose text NOT NULL DEFAULT 'association',
    ADD COLUMN IF NOT EXISTS replaces_credential_id uuid REFERENCES lighter_credentials(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS registered_public_key text;

ALTER TABLE lighter_credentials
    DROP CONSTRAINT IF EXISTS lighter_credentials_status_check;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'lighter_credentials_status_v2_check'
          AND conrelid = 'lighter_credentials'::regclass
    ) THEN
        ALTER TABLE lighter_credentials
            ADD CONSTRAINT lighter_credentials_status_v2_check
            CHECK (status IN (
                'generating', 'pending', 'verifying', 'linked',
                'superseded', 'blocked', 'revoked'
            ));
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'lighter_credentials_purpose_check'
          AND conrelid = 'lighter_credentials'::regclass
    ) THEN
        ALTER TABLE lighter_credentials
            ADD CONSTRAINT lighter_credentials_purpose_check
            CHECK (
                (purpose = 'association' AND replaces_credential_id IS NULL)
                OR (purpose = 'revocation' AND replaces_credential_id IS NOT NULL)
            );
    END IF;
END;
$$;

ALTER TABLE lighter_credential_bindings
    DROP CONSTRAINT IF EXISTS lighter_credential_bindings_status_check;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'lighter_credential_bindings_status_v2_check'
          AND conrelid = 'lighter_credential_bindings'::regclass
    ) THEN
        ALTER TABLE lighter_credential_bindings
            ADD CONSTRAINT lighter_credential_bindings_status_v2_check
            CHECK (status IN (
                'pending', 'rotation_pending', 'verifying', 'linked', 'blocked',
                'revocation_pending', 'revoking', 'revoked'
            ));
    END IF;
END;
$$;
