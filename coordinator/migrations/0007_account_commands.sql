ALTER TABLE execution_api_nonces
    DROP CONSTRAINT execution_api_nonces_scope_check;

ALTER TABLE execution_api_nonces
    ADD CONSTRAINT execution_api_nonces_scope_check
        CHECK (scope IN (
            'intent', 'exit', 'recovery', 'venue_event', 'market_quote', 'account_snapshot',
            'account_command'
        ));

ALTER TABLE execution_accounts
    ADD COLUMN owner_address TEXT CHECK (owner_address ~ '^0x[0-9a-f]{40}$'),
    ADD COLUMN strategy_manifest_sha256 TEXT
        CHECK (strategy_manifest_sha256 ~ '^[0-9a-f]{64}$');

CREATE TABLE execution_strategy_control (
    strategy_version TEXT PRIMARY KEY,
    strategy_manifest_sha256 TEXT CHECK (strategy_manifest_sha256 ~ '^[0-9a-f]{64}$'),
    mode TEXT NOT NULL CHECK (mode IN ('ACTIVE', 'REDUCE_ONLY', 'HALTED')),
    reason TEXT NOT NULL,
    version BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO execution_strategy_control
    (strategy_version, strategy_manifest_sha256, mode, reason)
SELECT DISTINCT strategy_version, strategy_manifest_sha256,
       'HALTED', 'strategy activation requires explicit approval'
FROM execution_accounts;

CREATE TABLE execution_account_commands (
    command_id TEXT PRIMARY KEY
        CHECK (command_id ~ '^[a-z0-9][a-z0-9-]{7,63}$'),
    execution_account_id TEXT NOT NULL REFERENCES execution_accounts(execution_account_id),
    agent_id TEXT NOT NULL,
    command TEXT NOT NULL CHECK (command IN ('launch', 'pause', 'resume', 'close', 'withdraw')),
    request_sha256 TEXT NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    status TEXT NOT NULL CHECK (status IN (
        'processing', 'reducing', 'awaiting_owner_signature', 'completed', 'blocked'
    )),
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (agent_id ~ '^[a-z0-9][a-z0-9-]{7,63}$')
);

CREATE INDEX execution_account_commands_account_status
    ON execution_account_commands (execution_account_id, status, created_at);

CREATE TABLE execution_account_command_events (
    id BIGSERIAL PRIMARY KEY,
    command_id TEXT NOT NULL REFERENCES execution_account_commands(command_id),
    status TEXT NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER execution_account_command_events_append_only
    BEFORE UPDATE OR DELETE ON execution_account_command_events
    FOR EACH ROW EXECUTE FUNCTION execution_reject_mutation();
