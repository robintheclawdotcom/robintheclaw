CREATE TYPE paper_evaluation_status AS ENUM ('candidate', 'declined');
CREATE TYPE paper_episode_status AS ENUM ('active', 'closed');

CREATE TABLE paper_agent_cursors (
    consumer TEXT NOT NULL,
    symbol TEXT NOT NULL,
    last_received_at TIMESTAMPTZ NOT NULL,
    last_event_id UUID NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, symbol)
);

CREATE INDEX raw_market_events_paper_cursor_idx
    ON raw_market_events (source, kind, symbol, received_at DESC, id DESC);

CREATE TABLE paper_opportunity_episodes (
    id UUID PRIMARY KEY,
    dedupe_key CHAR(64) NOT NULL UNIQUE,
    strategy_version TEXT NOT NULL,
    symbol TEXT NOT NULL,
    direction TEXT NOT NULL CHECK (direction = 'long_spot_short_perp'),
    status paper_episode_status NOT NULL,
    first_event_id UUID NOT NULL REFERENCES raw_market_events(id) ON DELETE RESTRICT,
    latest_event_id UUID NOT NULL REFERENCES raw_market_events(id) ON DELETE RESTRICT,
    opened_at TIMESTAMPTZ NOT NULL,
    last_observed_at TIMESTAMPTZ NOT NULL,
    closed_at TIMESTAMPTZ,
    evaluation_count BIGINT NOT NULL DEFAULT 1 CHECK (evaluation_count > 0),
    latest_net_edge_ppm BIGINT NOT NULL,
    stock_amount_raw TEXT NOT NULL CHECK (stock_amount_raw ~ '^[0-9]+$'),
    perp_quantity_wei TEXT NOT NULL CHECK (perp_quantity_wei ~ '^[0-9]+$'),
    entry_spot_cost_raw TEXT NOT NULL CHECK (entry_spot_cost_raw ~ '^[0-9]+$'),
    entry_spot_price_micros BIGINT NOT NULL CHECK (entry_spot_price_micros > 0),
    entry_perp_price_micros BIGINT NOT NULL CHECK (entry_perp_price_micros > 0),
    entry_perp_fee_raw TEXT NOT NULL CHECK (entry_perp_fee_raw ~ '^[0-9]+$'),
    gas_cost_per_leg_raw TEXT NOT NULL CHECK (gas_cost_per_leg_raw ~ '^[0-9]+$'),
    latest_spot_exit_raw TEXT,
    latest_perp_ask_micros BIGINT,
    unrealized_pnl_raw BIGINT,
    realized_pnl_raw BIGINT,
    close_reason TEXT,
    CHECK (
        (status = 'active' AND closed_at IS NULL)
        OR (status = 'closed' AND closed_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX paper_one_active_episode_per_market_idx
    ON paper_opportunity_episodes (strategy_version, symbol)
    WHERE status = 'active';

CREATE TABLE paper_market_state (
    strategy_version TEXT NOT NULL,
    symbol TEXT NOT NULL,
    active_episode_id UUID REFERENCES paper_opportunity_episodes(id) ON DELETE RESTRICT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (strategy_version, symbol)
);

CREATE TABLE paper_evaluations (
    id UUID PRIMARY KEY,
    strategy_version TEXT NOT NULL,
    event_id UUID NOT NULL REFERENCES raw_market_events(id) ON DELETE RESTRICT,
    symbol TEXT NOT NULL,
    status paper_evaluation_status NOT NULL,
    reason TEXT,
    direction TEXT CHECK (direction IS NULL OR direction = 'long_spot_short_perp'),
    episode_id UUID REFERENCES paper_opportunity_episodes(id) ON DELETE RESTRICT,
    block_number BIGINT,
    block_hash TEXT,
    gross_edge_ppm BIGINT,
    net_edge_ppm BIGINT,
    evidence JSONB NOT NULL,
    evaluated_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (strategy_version, event_id),
    CHECK (
        (status = 'candidate' AND direction = 'long_spot_short_perp'
            AND gross_edge_ppm IS NOT NULL AND net_edge_ppm IS NOT NULL)
        OR status = 'declined'
    )
);

CREATE INDEX paper_evaluations_symbol_time_idx
    ON paper_evaluations (strategy_version, symbol, evaluated_at DESC);
CREATE INDEX paper_episodes_time_idx
    ON paper_opportunity_episodes (strategy_version, opened_at DESC);

COMMENT ON TABLE paper_evaluations IS
    'One immutable evaluation for each configured Lighter ticker event consumed by the paper agent.';
COMMENT ON TABLE paper_opportunity_episodes IS
    'Contiguous candidate windows; repeated ticks update one active episode rather than creating trades.';
