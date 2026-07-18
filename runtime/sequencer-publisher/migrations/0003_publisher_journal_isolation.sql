ALTER TABLE sequencer_publisher_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE sequencer_publisher_state FORCE ROW LEVEL SECURITY;
ALTER TABLE sequencer_publisher_transactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE sequencer_publisher_transactions FORCE ROW LEVEL SECURITY;
ALTER TABLE aapl_relay_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE aapl_relay_state FORCE ROW LEVEL SECURITY;
ALTER TABLE aapl_relay_transactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE aapl_relay_transactions FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS sequencer_publisher_identity ON sequencer_publisher_state;
CREATE POLICY sequencer_publisher_identity ON sequencer_publisher_state
    USING (
        pg_has_role(current_user, 'pg_database_owner', 'MEMBER')
        OR current_user = 'robin_execution_readonly'
        OR (current_user = 'robin_execution_sequencer_1'
            AND publisher_id = 'sequencer-publisher-1')
        OR (current_user = 'robin_execution_sequencer_2'
            AND publisher_id = 'sequencer-publisher-2')
        OR (current_user = 'robin_execution_sequencer_3'
            AND publisher_id = 'sequencer-publisher-3')
    )
    WITH CHECK (
        pg_has_role(current_user, 'pg_database_owner', 'MEMBER')
        OR (current_user = 'robin_execution_sequencer_1'
            AND publisher_id = 'sequencer-publisher-1')
        OR (current_user = 'robin_execution_sequencer_2'
            AND publisher_id = 'sequencer-publisher-2')
        OR (current_user = 'robin_execution_sequencer_3'
            AND publisher_id = 'sequencer-publisher-3')
    );

DROP POLICY IF EXISTS sequencer_transaction_identity ON sequencer_publisher_transactions;
CREATE POLICY sequencer_transaction_identity ON sequencer_publisher_transactions
    USING (
        pg_has_role(current_user, 'pg_database_owner', 'MEMBER')
        OR current_user = 'robin_execution_readonly'
        OR (current_user = 'robin_execution_sequencer_1'
            AND publisher_id = 'sequencer-publisher-1')
        OR (current_user = 'robin_execution_sequencer_2'
            AND publisher_id = 'sequencer-publisher-2')
        OR (current_user = 'robin_execution_sequencer_3'
            AND publisher_id = 'sequencer-publisher-3')
    )
    WITH CHECK (
        pg_has_role(current_user, 'pg_database_owner', 'MEMBER')
        OR (current_user = 'robin_execution_sequencer_1'
            AND publisher_id = 'sequencer-publisher-1')
        OR (current_user = 'robin_execution_sequencer_2'
            AND publisher_id = 'sequencer-publisher-2')
        OR (current_user = 'robin_execution_sequencer_3'
            AND publisher_id = 'sequencer-publisher-3')
    );

DROP POLICY IF EXISTS aapl_relay_identity ON aapl_relay_state;
CREATE POLICY aapl_relay_identity ON aapl_relay_state
    USING (
        pg_has_role(current_user, 'pg_database_owner', 'MEMBER')
        OR current_user = 'robin_execution_readonly'
        OR (current_user = 'robin_execution_aapl_relay_1'
            AND publisher_id = 'aapl-relay-1')
        OR (current_user = 'robin_execution_aapl_relay_2'
            AND publisher_id = 'aapl-relay-2')
        OR (current_user = 'robin_execution_aapl_relay_3'
            AND publisher_id = 'aapl-relay-3')
    )
    WITH CHECK (
        pg_has_role(current_user, 'pg_database_owner', 'MEMBER')
        OR (current_user = 'robin_execution_aapl_relay_1'
            AND publisher_id = 'aapl-relay-1')
        OR (current_user = 'robin_execution_aapl_relay_2'
            AND publisher_id = 'aapl-relay-2')
        OR (current_user = 'robin_execution_aapl_relay_3'
            AND publisher_id = 'aapl-relay-3')
    );

DROP POLICY IF EXISTS aapl_relay_transaction_identity ON aapl_relay_transactions;
CREATE POLICY aapl_relay_transaction_identity ON aapl_relay_transactions
    USING (
        pg_has_role(current_user, 'pg_database_owner', 'MEMBER')
        OR current_user = 'robin_execution_readonly'
        OR (current_user = 'robin_execution_aapl_relay_1'
            AND publisher_id = 'aapl-relay-1')
        OR (current_user = 'robin_execution_aapl_relay_2'
            AND publisher_id = 'aapl-relay-2')
        OR (current_user = 'robin_execution_aapl_relay_3'
            AND publisher_id = 'aapl-relay-3')
    )
    WITH CHECK (
        pg_has_role(current_user, 'pg_database_owner', 'MEMBER')
        OR (current_user = 'robin_execution_aapl_relay_1'
            AND publisher_id = 'aapl-relay-1')
        OR (current_user = 'robin_execution_aapl_relay_2'
            AND publisher_id = 'aapl-relay-2')
        OR (current_user = 'robin_execution_aapl_relay_3'
            AND publisher_id = 'aapl-relay-3')
    );
