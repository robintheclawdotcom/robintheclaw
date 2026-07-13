ALTER TABLE execution_intents
    ADD COLUMN payload_sha256 TEXT
        CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    ADD COLUMN payload_digest_required BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE execution_intents
    ALTER COLUMN payload_digest_required SET DEFAULT TRUE,
    ADD CONSTRAINT execution_intents_required_payload_digest
        CHECK (NOT payload_digest_required OR payload_sha256 IS NOT NULL);
