CREATE TYPE market_event_kind AS ENUM (
    'order_book',
    'ticker',
    'trade',
    'funding',
    'open_interest',
    'market_stats',
    'chain_block',
    'sequencer',
    'pool_state',
    'source_health'
);

CREATE TYPE finality_state AS ENUM ('pending', 'confirmed', 'finalized', 'not_applicable');

CREATE TYPE shadow_intent_status AS ENUM (
    'declined',
    'proposed',
    'partially_hedged',
    'hedged',
    'unhedged',
    'cancelled',
    'expired',
    'unwound',
    'stale'
);

CREATE TABLE raw_market_events (
    id UUID PRIMARY KEY,
    source TEXT NOT NULL,
    connector_version TEXT NOT NULL,
    kind market_event_kind NOT NULL,
    symbol TEXT,
    source_timestamp_ms BIGINT,
    received_at TIMESTAMPTZ NOT NULL,
    source_sequence TEXT,
    block_number BIGINT,
    block_hash TEXT,
    finality finality_state NOT NULL,
    payload_sha256 CHAR(64) NOT NULL,
    raw_object_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, connector_version, payload_sha256)
);

CREATE INDEX raw_market_events_symbol_time_idx
    ON raw_market_events (symbol, received_at DESC);
CREATE INDEX raw_market_events_kind_time_idx
    ON raw_market_events (kind, received_at DESC);

CREATE TABLE market_features (
    event_id UUID PRIMARY KEY REFERENCES raw_market_events(id) ON DELETE RESTRICT,
    symbol TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    spot_bid NUMERIC,
    spot_ask NUMERIC,
    perp_bid NUMERIC,
    perp_ask NUMERIC,
    perp_mark NUMERIC,
    perp_index NUMERIC,
    funding_rate NUMERIC,
    open_interest NUMERIC,
    gas_usd NUMERIC,
    quote_age_ms BIGINT,
    source_health JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX market_features_symbol_time_idx
    ON market_features (symbol, observed_at DESC);

CREATE TABLE dataset_snapshots (
    id UUID PRIMARY KEY,
    manifest_sha256 CHAR(64) NOT NULL UNIQUE,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    source_filter JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at)
);

CREATE TABLE strategy_candidates (
    id UUID PRIMARY KEY,
    version TEXT NOT NULL UNIQUE,
    hypothesis TEXT NOT NULL,
    parameters JSONB NOT NULL,
    dataset_snapshot_id UUID REFERENCES dataset_snapshots(id) ON DELETE RESTRICT,
    registered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    status TEXT NOT NULL CHECK (status IN ('registered', 'evaluating', 'shadow', 'rejected', 'retired'))
);

CREATE TABLE shadow_intents (
    id UUID PRIMARY KEY,
    strategy_id UUID NOT NULL REFERENCES strategy_candidates(id) ON DELETE RESTRICT,
    event_id UUID NOT NULL REFERENCES raw_market_events(id) ON DELETE RESTRICT,
    dedupe_key CHAR(64) NOT NULL UNIQUE,
    symbol TEXT NOT NULL,
    status shadow_intent_status NOT NULL,
    decision JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    reason TEXT
);

CREATE INDEX shadow_intents_strategy_time_idx
    ON shadow_intents (strategy_id, created_at DESC);

CREATE TABLE shadow_legs (
    id UUID PRIMARY KEY,
    intent_id UUID NOT NULL REFERENCES shadow_intents(id) ON DELETE CASCADE,
    venue TEXT NOT NULL,
    side TEXT NOT NULL CHECK (side IN ('buy', 'sell')),
    requested_notional_usd NUMERIC NOT NULL CHECK (requested_notional_usd > 0),
    simulated_fill_notional_usd NUMERIC NOT NULL CHECK (simulated_fill_notional_usd >= 0),
    simulated_price NUMERIC,
    fee_usd NUMERIC NOT NULL CHECK (fee_usd >= 0),
    impact_bps NUMERIC NOT NULL CHECK (impact_bps >= 0),
    status shadow_intent_status NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE source_health (
    source TEXT PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('healthy', 'degraded', 'offline')),
    last_event_at TIMESTAMPTZ,
    last_error TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE risk_snapshots (
    id UUID PRIMARY KEY,
    observed_at TIMESTAMPTZ NOT NULL,
    strategy_id UUID REFERENCES strategy_candidates(id) ON DELETE SET NULL,
    state JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
