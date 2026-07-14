ALTER TABLE execution_account_bindings
    ADD COLUMN robinhood_authorization_transaction_hash text CHECK (
        robinhood_authorization_transaction_hash IS NULL
        OR robinhood_authorization_transaction_hash ~ '^0x[0-9a-f]{64}$'
    ),
    ADD COLUMN robinhood_authorization_block bigint CHECK (
        robinhood_authorization_block IS NULL OR robinhood_authorization_block > 0
    );

CREATE UNIQUE INDEX execution_bindings_robinhood_authorization_tx_uq
    ON execution_account_bindings(lower(robinhood_authorization_transaction_hash))
    WHERE venue = 'robinhood' AND robinhood_authorization_transaction_hash IS NOT NULL;
