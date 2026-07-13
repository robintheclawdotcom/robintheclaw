CREATE TABLE users (
    id uuid PRIMARY KEY,
    privy_did text NOT NULL UNIQUE,
    onboarding_state text NOT NULL DEFAULT 'account',
    has_recovery boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE wallet_links (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chain_namespace text NOT NULL,
    address text NOT NULL,
    wallet_type text NOT NULL,
    label text,
    is_primary boolean NOT NULL DEFAULT false,
    verified_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (chain_namespace, address),
    UNIQUE (user_id, chain_namespace, address)
);

CREATE INDEX wallet_links_user_idx ON wallet_links(user_id);

CREATE TABLE smart_accounts (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chain_id bigint NOT NULL,
    address text NOT NULL,
    provider text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, chain_id),
    UNIQUE (chain_id, address)
);

CREATE TABLE vaults (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chain_id bigint NOT NULL,
    factory_version bigint NOT NULL,
    asset_address text NOT NULL,
    vault_address text NOT NULL,
    guard_address text NOT NULL,
    anchor_address text NOT NULL,
    call_id text NOT NULL UNIQUE,
    transaction_hash text NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, chain_id, factory_version)
);

CREATE TABLE activity (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chain_id bigint NOT NULL,
    kind text NOT NULL,
    transaction_hash text,
    block_number bigint,
    log_index bigint,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX activity_log_identity_idx
    ON activity(chain_id, transaction_hash, log_index)
    WHERE transaction_hash IS NOT NULL AND log_index IS NOT NULL;
CREATE INDEX activity_user_time_idx ON activity(user_id, occurred_at DESC, id DESC);

CREATE TABLE app_cursors (
    name text PRIMARY KEY,
    block_number bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE product_metrics (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    duration_ms bigint,
    status text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX product_metrics_name_time_idx ON product_metrics(name, created_at DESC);

CREATE TABLE preferences (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    display_currency text NOT NULL DEFAULT 'USD',
    active_funding_wallet text,
    notifications_enabled boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now()
);
