CREATE TABLE agents (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    strategy_version text NOT NULL,
    mode text NOT NULL CHECK (mode IN ('paper', 'live')),
    status text NOT NULL CHECK (status IN ('running', 'paused')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE agent_paper_events (
    id uuid PRIMARY KEY,
    agent_id uuid NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    evaluation_id uuid NOT NULL,
    market_event_id uuid NOT NULL,
    episode_id uuid,
    symbol text NOT NULL,
    status text NOT NULL CHECK (status IN ('candidate', 'declined')),
    reason text,
    net_edge_ppm bigint,
    evaluated_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (agent_id, evaluation_id)
);

CREATE INDEX agent_paper_events_agent_time_idx
    ON agent_paper_events(agent_id, evaluated_at DESC);
CREATE INDEX agent_paper_events_open_idx
    ON agent_paper_events(agent_id, episode_id)
    WHERE episode_id IS NOT NULL;
