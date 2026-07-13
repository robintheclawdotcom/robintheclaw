ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_status_check;
ALTER TABLE agents
    ADD CONSTRAINT agents_status_check CHECK (status IN (
        'setup',
        'provisioning',
        'awaiting_signatures',
        'awaiting_funding',
        'ready',
        'running',
        'reducing',
        'paused',
        'closing',
        'closed',
        'blocked'
    ));
ALTER TABLE agents ADD COLUMN blocked_reason text;

CREATE TABLE execution_accounts (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    agent_id uuid NOT NULL UNIQUE REFERENCES agents(id) ON DELETE CASCADE,
    strategy_version text NOT NULL CHECK (strategy_version = 'basis-aapl-v1'),
    chain_id bigint NOT NULL DEFAULT 4663 CHECK (chain_id = 4663),
    status text NOT NULL CHECK (status IN (
        'provisioning',
        'awaiting_signatures',
        'awaiting_funding',
        'ready',
        'blocked',
        'closed'
    )),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE execution_account_bindings (
    id uuid PRIMARY KEY,
    execution_account_id uuid NOT NULL REFERENCES execution_accounts(id) ON DELETE CASCADE,
    venue text NOT NULL CHECK (venue IN ('lighter', 'robinhood')),
    binding_ref uuid NOT NULL UNIQUE,
    request_id uuid NOT NULL UNIQUE,
    owner_address text NOT NULL,
    public_identifier text,
    public_key text,
    association_payload text,
    proof_transaction_hash text,
    status text NOT NULL CHECK (status IN ('provisioning', 'awaiting_signature', 'verifying', 'linked', 'rejected')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (execution_account_id, venue)
);

CREATE TABLE agent_readiness (
    execution_account_id uuid PRIMARY KEY REFERENCES execution_accounts(id) ON DELETE CASCADE,
    lighter_linked boolean NOT NULL DEFAULT false,
    lighter_funded boolean NOT NULL DEFAULT false,
    robinhood_deployed boolean NOT NULL DEFAULT false,
    robinhood_funded boolean NOT NULL DEFAULT false,
    user_gas_ready boolean NOT NULL DEFAULT false,
    execution_gas_ready boolean NOT NULL DEFAULT false,
    policy_active boolean NOT NULL DEFAULT false,
    reconciled boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE agent_readiness_snapshots (
    id uuid PRIMARY KEY,
    execution_account_id uuid NOT NULL REFERENCES execution_accounts(id) ON DELETE CASCADE,
    lighter_linked boolean NOT NULL,
    lighter_funded boolean NOT NULL,
    robinhood_deployed boolean NOT NULL,
    robinhood_funded boolean NOT NULL,
    user_gas_ready boolean NOT NULL,
    execution_gas_ready boolean NOT NULL,
    policy_active boolean NOT NULL,
    reconciled boolean NOT NULL,
    observed_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX agent_readiness_snapshots_account_time_idx
    ON agent_readiness_snapshots(execution_account_id, observed_at DESC, id DESC);

CREATE TABLE agent_commands (
    id uuid PRIMARY KEY,
    agent_id uuid NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    execution_account_id uuid NOT NULL REFERENCES execution_accounts(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    command text NOT NULL CHECK (command IN ('launch', 'pause', 'resume', 'close', 'withdraw')),
    status text NOT NULL CHECK (status IN ('accepted', 'completed', 'rejected')),
    agent_status text NOT NULL,
    error_reason text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (agent_id, idempotency_key)
);

CREATE INDEX agent_commands_account_time_idx
    ON agent_commands(execution_account_id, created_at DESC, id DESC);
