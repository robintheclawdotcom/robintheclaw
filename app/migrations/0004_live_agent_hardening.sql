ALTER TABLE execution_account_bindings
    ADD COLUMN provider_request_id uuid,
    ADD COLUMN lighter_account_index bigint CHECK (lighter_account_index IS NULL OR lighter_account_index > 0),
    ADD COLUMN lighter_api_key_index smallint CHECK (
        lighter_api_key_index IS NULL OR lighter_api_key_index BETWEEN 2 AND 254
    ),
    ADD COLUMN robinhood_vault_address text;

ALTER TABLE execution_accounts
    ADD COLUMN strategy_manifest_sha256 text NOT NULL
        DEFAULT '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a'
        CHECK (strategy_manifest_sha256 = '4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a');
ALTER TABLE execution_accounts ALTER COLUMN strategy_manifest_sha256 DROP DEFAULT;

CREATE UNIQUE INDEX execution_bindings_provider_request_uq
    ON execution_account_bindings(venue, provider_request_id)
    WHERE provider_request_id IS NOT NULL;

CREATE UNIQUE INDEX execution_bindings_lighter_account_uq
    ON execution_account_bindings(lighter_account_index)
    WHERE venue = 'lighter' AND lighter_account_index IS NOT NULL
      AND status IN ('awaiting_signature', 'verifying', 'linked');

CREATE UNIQUE INDEX execution_bindings_robinhood_vault_uq
    ON execution_account_bindings(lower(robinhood_vault_address))
    WHERE venue = 'robinhood' AND robinhood_vault_address IS NOT NULL
      AND status IN ('verifying', 'linked');

CREATE UNIQUE INDEX execution_bindings_public_identifier_uq
    ON execution_account_bindings(venue, lower(public_identifier))
    WHERE public_identifier IS NOT NULL AND status IN ('verifying', 'linked');

CREATE UNIQUE INDEX execution_bindings_public_key_uq
    ON execution_account_bindings(lower(public_key))
    WHERE venue = 'lighter' AND public_key IS NOT NULL AND status IN ('awaiting_signature', 'verifying', 'linked');

CREATE UNIQUE INDEX execution_bindings_proof_tx_uq
    ON execution_account_bindings(venue, lower(proof_transaction_hash))
    WHERE proof_transaction_hash IS NOT NULL AND status IN ('verifying', 'linked');

CREATE TABLE agent_readiness_evidence (
    id uuid PRIMARY KEY,
    execution_account_id uuid NOT NULL REFERENCES execution_accounts(id) ON DELETE RESTRICT,
    snapshot_id uuid NOT NULL,
    check_name text NOT NULL CHECK (check_name IN (
        'lighter_linked',
        'lighter_funded',
        'robinhood_deployed',
        'robinhood_funded',
        'user_gas_ready',
        'execution_gas_ready',
        'policy_active',
        'reconciled'
    )),
    ready boolean NOT NULL,
    source text NOT NULL CHECK (length(source) BETWEEN 1 AND 128),
    evidence_digest text NOT NULL CHECK (evidence_digest ~ '^[0-9a-f]{64}$'),
    observed_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (snapshot_id, check_name),
    CHECK (expires_at > observed_at),
    CHECK (
        expires_at <= observed_at + CASE
            WHEN check_name IN ('lighter_linked', 'robinhood_deployed') THEN interval '24 hours'
            ELSE interval '60 seconds'
        END
    )
);

CREATE INDEX agent_readiness_evidence_account_check_time_idx
    ON agent_readiness_evidence(execution_account_id, check_name, observed_at DESC, id DESC);

CREATE INDEX agent_readiness_evidence_account_snapshot_idx
    ON agent_readiness_evidence(execution_account_id, snapshot_id, observed_at DESC);

CREATE FUNCTION reject_readiness_history_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'readiness history is append-only';
END;
$$;

CREATE TRIGGER agent_readiness_evidence_append_only
    BEFORE UPDATE OR DELETE ON agent_readiness_evidence
    FOR EACH ROW EXECUTE FUNCTION reject_readiness_history_mutation();

CREATE TRIGGER agent_readiness_snapshots_append_only
    BEFORE UPDATE OR DELETE ON agent_readiness_snapshots
    FOR EACH ROW EXECUTE FUNCTION reject_readiness_history_mutation();

INSERT INTO agent_readiness_evidence (
    id, execution_account_id, snapshot_id, check_name, ready, source, evidence_digest,
    observed_at, expires_at
)
SELECT gen_random_uuid(), account.id, account.id, checks.check_name, false, 'migration-fail-closed',
       repeat('0', 64), now(), now() + interval '1 second'
FROM execution_accounts account
CROSS JOIN unnest(ARRAY[
    'lighter_linked',
    'lighter_funded',
    'robinhood_deployed',
    'robinhood_funded',
    'user_gas_ready',
    'execution_gas_ready',
    'policy_active',
    'reconciled'
]) AS checks(check_name);

CREATE VIEW current_agent_readiness AS
WITH complete_snapshots AS (
    SELECT execution_account_id, snapshot_id, max(observed_at) AS observed_at
    FROM agent_readiness_evidence
    GROUP BY execution_account_id, snapshot_id
    HAVING count(DISTINCT check_name) = 8
), latest_snapshot AS (
    SELECT DISTINCT ON (execution_account_id)
        execution_account_id, snapshot_id
    FROM complete_snapshots
    ORDER BY execution_account_id, observed_at DESC, snapshot_id DESC
), latest AS (
    SELECT evidence.execution_account_id, evidence.check_name, evidence.ready,
           evidence.observed_at, evidence.expires_at
    FROM agent_readiness_evidence evidence
    JOIN latest_snapshot snapshot
      ON snapshot.execution_account_id = evidence.execution_account_id
     AND snapshot.snapshot_id = evidence.snapshot_id
)
SELECT
    account.id AS execution_account_id,
    coalesce(bool_or(ready AND expires_at > now()) FILTER (WHERE check_name = 'lighter_linked'), false) AS lighter_linked,
    coalesce(bool_or(ready AND expires_at > now()) FILTER (WHERE check_name = 'lighter_funded'), false) AS lighter_funded,
    coalesce(bool_or(ready AND expires_at > now()) FILTER (WHERE check_name = 'robinhood_deployed'), false) AS robinhood_deployed,
    coalesce(bool_or(ready AND expires_at > now()) FILTER (WHERE check_name = 'robinhood_funded'), false) AS robinhood_funded,
    coalesce(bool_or(ready AND expires_at > now()) FILTER (WHERE check_name = 'user_gas_ready'), false) AS user_gas_ready,
    coalesce(bool_or(ready AND expires_at > now()) FILTER (WHERE check_name = 'execution_gas_ready'), false) AS execution_gas_ready,
    coalesce(bool_or(ready AND expires_at > now()) FILTER (WHERE check_name = 'policy_active'), false) AS policy_active,
    coalesce(bool_or(ready AND expires_at > now()) FILTER (WHERE check_name = 'reconciled'), false) AS reconciled,
    min(expires_at) FILTER (WHERE ready AND expires_at > now()) AS valid_until
FROM execution_accounts account
LEFT JOIN latest ON latest.execution_account_id = account.id
GROUP BY account.id;

ALTER TABLE agent_commands DROP CONSTRAINT agent_commands_status_check;
UPDATE agent_commands
SET status = 'failed',
    error_reason = 'command predates durable execution dispatch',
    updated_at = now()
WHERE status = 'accepted';
ALTER TABLE agent_commands
    ADD CONSTRAINT agent_commands_status_check CHECK (status IN (
        'pending', 'processing', 'awaiting_signature', 'completed', 'rejected', 'failed'
    )),
    ADD COLUMN target_agent_status text NOT NULL DEFAULT 'blocked',
    ADD COLUMN result_evidence_digest text CHECK (
        result_evidence_digest IS NULL OR result_evidence_digest ~ '^[0-9a-f]{64}$'
    ),
    ADD COLUMN completed_at timestamptz;

UPDATE agent_commands SET target_agent_status = agent_status;
ALTER TABLE agent_commands ALTER COLUMN target_agent_status DROP DEFAULT;

CREATE UNIQUE INDEX agent_commands_one_inflight_uq
    ON agent_commands(agent_id)
    WHERE status IN ('pending', 'processing', 'awaiting_signature');

CREATE TABLE agent_command_outbox (
    command_id uuid PRIMARY KEY REFERENCES agent_commands(id) ON DELETE RESTRICT,
    available_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    claimed_by text,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    delivered_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (claimed_by IS NULL OR length(claimed_by) BETWEEN 1 AND 128)
);

CREATE INDEX agent_command_outbox_pending_idx
    ON agent_command_outbox(available_at, command_id)
    WHERE delivered_at IS NULL;

CREATE TABLE app_internal_nonces (
    scope text NOT NULL CHECK (scope IN ('readiness')),
    caller text NOT NULL CHECK (length(caller) BETWEEN 3 AND 64),
    nonce text NOT NULL CHECK (length(nonce) BETWEEN 32 AND 128),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, caller, nonce)
);

CREATE INDEX app_internal_nonces_expiry_idx ON app_internal_nonces(expires_at);
