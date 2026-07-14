CREATE TABLE IF NOT EXISTS lighter_signing_requests (
    credential_id uuid NOT NULL REFERENCES lighter_credentials(id) ON DELETE RESTRICT,
    execution_account_id uuid NOT NULL,
    nonce bigint NOT NULL CHECK (nonce BETWEEN 0 AND 281474976710655),
    intent_id text NOT NULL CHECK (length(intent_id) BETWEEN 8 AND 128),
    request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    status text NOT NULL CHECK (status IN ('claimed', 'signed')),
    signed_transaction jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    signed_at timestamptz,
    PRIMARY KEY (credential_id, nonce),
    CHECK (
        (status = 'claimed' AND signed_transaction IS NULL AND signed_at IS NULL)
        OR (status = 'signed' AND jsonb_typeof(signed_transaction) = 'object' AND signed_at IS NOT NULL)
    )
);

CREATE OR REPLACE FUNCTION enforce_lighter_signing_request_transition()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Lighter signing requests cannot be deleted';
    END IF;
    IF OLD.status <> 'claimed' OR NEW.status <> 'signed'
       OR OLD.credential_id <> NEW.credential_id
       OR OLD.execution_account_id <> NEW.execution_account_id
       OR OLD.nonce <> NEW.nonce
       OR OLD.intent_id <> NEW.intent_id
       OR OLD.request_sha256 <> NEW.request_sha256
       OR OLD.created_at <> NEW.created_at THEN
        RAISE EXCEPTION 'invalid Lighter signing request transition';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS lighter_signing_requests_monotonic ON lighter_signing_requests;
CREATE TRIGGER lighter_signing_requests_monotonic
    BEFORE UPDATE OR DELETE ON lighter_signing_requests
    FOR EACH ROW EXECUTE FUNCTION enforce_lighter_signing_request_transition();

CREATE UNIQUE INDEX IF NOT EXISTS lighter_credential_audit_signing_nonce_claim
    ON lighter_credential_audit (credential_id, (details->>'nonce'))
    WHERE event = 'transaction_nonce_claimed';
