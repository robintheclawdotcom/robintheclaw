CREATE TABLE live_evaluation_order_cursors (
    execution_account_id TEXT PRIMARY KEY
        REFERENCES execution_accounts(execution_account_id),
    next_order_index BIGINT NOT NULL DEFAULT 1
        CHECK (next_order_index BETWEEN 1 AND 1099511627776),
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE live_evaluation_order_allocations (
    evaluation_id TEXT NOT NULL,
    execution_account_id TEXT NOT NULL,
    client_order_index BIGINT NOT NULL CHECK (
        client_order_index BETWEEN 1 AND 1099511627772
    ),
    unwind_order_index BIGINT NOT NULL CHECK (
        unwind_order_index = client_order_index + 1
        AND unwind_order_index BETWEEN 2 AND 1099511627773
    ),
    unwind_order_count SMALLINT NOT NULL CHECK (unwind_order_count = 3),
    allocated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (evaluation_id, execution_account_id),
    UNIQUE (execution_account_id, client_order_index),
    UNIQUE (execution_account_id, unwind_order_index),
    FOREIGN KEY (evaluation_id, execution_account_id)
        REFERENCES live_scheduler_approvals(evaluation_id, execution_account_id)
);

CREATE OR REPLACE FUNCTION live_evaluation_reject_allocation_mutation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'live evaluation allocation journal is append-only';
END;
$$;

CREATE TRIGGER live_evaluation_order_allocations_append_only
    BEFORE UPDATE OR DELETE ON live_evaluation_order_allocations
    FOR EACH ROW EXECUTE FUNCTION live_evaluation_reject_allocation_mutation();
