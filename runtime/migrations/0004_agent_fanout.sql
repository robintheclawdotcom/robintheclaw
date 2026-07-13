CREATE TABLE agent_fanout_outbox (
    evaluation_id UUID PRIMARY KEY REFERENCES paper_evaluations(id) ON DELETE RESTRICT,
    strategy_version TEXT NOT NULL,
    market_event_id UUID NOT NULL REFERENCES raw_market_events(id) ON DELETE RESTRICT,
    episode_id UUID REFERENCES paper_opportunity_episodes(id) ON DELETE RESTRICT,
    symbol TEXT NOT NULL,
    status paper_evaluation_status NOT NULL,
    reason TEXT,
    net_edge_ppm BIGINT,
    evaluated_at TIMESTAMPTZ NOT NULL,
    delivered_at TIMESTAMPTZ,
    delivery_attempts BIGINT NOT NULL DEFAULT 0 CHECK (delivery_attempts >= 0),
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX agent_fanout_outbox_pending_idx
    ON agent_fanout_outbox(created_at)
    WHERE delivered_at IS NULL;
