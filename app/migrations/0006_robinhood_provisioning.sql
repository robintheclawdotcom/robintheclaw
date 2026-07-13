ALTER TABLE execution_account_bindings
    ADD COLUMN robinhood_signer_address text,
    ADD COLUMN robinhood_key_version bigint CHECK (
        robinhood_key_version IS NULL OR robinhood_key_version > 0
    ),
    ADD COLUMN robinhood_factory_address text,
    ADD COLUMN robinhood_registry_address text,
    ADD COLUMN robinhood_policy_digest text,
    ADD COLUMN robinhood_risk_manager_address text,
    ADD COLUMN robinhood_spot_adapter_address text,
    ADD COLUMN robinhood_deployment_block bigint CHECK (
        robinhood_deployment_block IS NULL OR robinhood_deployment_block > 0
    ),
    ADD COLUMN robinhood_deployment_action jsonb CHECK (
        robinhood_deployment_action IS NULL
        OR jsonb_typeof(robinhood_deployment_action) = 'object'
    );

CREATE UNIQUE INDEX execution_bindings_robinhood_signer_uq
    ON execution_account_bindings(lower(robinhood_signer_address))
    WHERE venue = 'robinhood' AND robinhood_signer_address IS NOT NULL
      AND status IN ('awaiting_signature', 'verifying', 'linked');

CREATE UNIQUE INDEX execution_bindings_robinhood_risk_manager_uq
    ON execution_account_bindings(lower(robinhood_risk_manager_address))
    WHERE venue = 'robinhood' AND robinhood_risk_manager_address IS NOT NULL
      AND status IN ('awaiting_signature', 'verifying', 'linked');

CREATE UNIQUE INDEX execution_bindings_robinhood_spot_adapter_uq
    ON execution_account_bindings(lower(robinhood_spot_adapter_address))
    WHERE venue = 'robinhood' AND robinhood_spot_adapter_address IS NOT NULL
      AND status IN ('awaiting_signature', 'verifying', 'linked');
