ALTER TABLE agent_commands
    ADD COLUMN dispatch_requested_at timestamptz,
    ADD COLUMN result_owner_actions jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(result_owner_actions) = 'array');
