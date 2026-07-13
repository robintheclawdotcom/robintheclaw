package strategyrunner

import "github.com/robin-the-claw/liveexec/protocol"

type SourceEvaluation struct {
	ID                     string          `json:"id"`
	StrategyVersion        string          `json:"strategy_version"`
	StrategyManifestSHA256 string          `json:"strategy_manifest_sha256"`
	SourceConfigSHA256     string          `json:"source_config_sha256"`
	DatasetManifest        string          `json:"dataset_manifest"`
	MarketManifest         string          `json:"market_manifest"`
	Status                 string          `json:"status"`
	Action                 protocol.Action `json:"action"`
	ObservedAtMS           uint64          `json:"observed_at_ms"`
	EstimatedCostMicros    uint64          `json:"estimated_cost_micros"`
}

type Readiness struct {
	ExecutionAccountID     string `json:"execution_account_id"`
	AgentID                string `json:"agent_id"`
	StrategyVersion        string `json:"strategy_version"`
	StrategyManifestSHA256 string `json:"strategy_manifest_sha256"`
	Lifecycle              string `json:"lifecycle"`
	GlobalControl          string `json:"global_control"`
	StrategyControl        string `json:"strategy_control"`
	AccountControl         string `json:"account_control"`
	FullyVerified          bool   `json:"fully_verified"`
	VaultWired             bool   `json:"vault_wired"`
	VaultFunded            bool   `json:"vault_funded"`
	ExecutionSignerFunded  bool   `json:"execution_signer_funded"`
	LighterLinked          bool   `json:"lighter_linked"`
	LighterFunded          bool   `json:"lighter_funded"`
	RouteHealthy           bool   `json:"route_healthy"`
	OracleHealthy          bool   `json:"oracle_healthy"`
	SequencerHealthy       bool   `json:"sequencer_healthy"`
	ObservedAtMS           uint64 `json:"observed_at_ms"`
}

type AccountState struct {
	ExecutionAccountID       string `json:"execution_account_id"`
	AgentID                  string `json:"agent_id"`
	StrategyManifestSHA256   string `json:"strategy_manifest_sha256"`
	LighterAccountIndex      uint64 `json:"lighter_account_index"`
	LighterAPIKeyIndex       uint8  `json:"lighter_api_key_index"`
	LighterMarketIndex       uint32 `json:"lighter_market_index"`
	LighterNonceAligned      bool   `json:"lighter_nonce_aligned"`
	UnknownLighterOrders     bool   `json:"unknown_lighter_orders"`
	UnknownLighterPositions  bool   `json:"unknown_lighter_positions"`
	CollateralMicros         uint64 `json:"collateral_micros"`
	MaintenanceMarginMicros  uint64 `json:"maintenance_margin_micros"`
	RobinhoodVault           string `json:"robinhood_vault"`
	RobinhoodSigner          string `json:"robinhood_signer"`
	RobinhoodNonceAligned    bool   `json:"robinhood_nonce_aligned"`
	UnknownRobinhoodPosition bool   `json:"unknown_robinhood_position"`
	NAVMicros                uint64 `json:"nav_micros"`
	DailyTurnoverMicros      uint64 `json:"daily_turnover_micros"`
	ActiveEpisodes           uint8  `json:"active_episodes"`
	Flat                     bool   `json:"flat"`
	SpotDecimals             uint8  `json:"spot_decimals"`
	SpotConfigVersion        uint64 `json:"spot_config_version"`
	UIMultiplierE18          string `json:"ui_multiplier_e18"`
	NextClientOrderIndex     uint64 `json:"next_client_order_index"`
	NextUnwindOrderIndex     uint64 `json:"next_unwind_order_index"`
	ObservedAtMS             uint64 `json:"observed_at_ms"`
}

type OpenEpisode struct {
	PairIntentID               string `json:"pair_intent_id"`
	SpotUnwindIntentID         string `json:"spot_unwind_intent_id"`
	SpotAmount                 string `json:"spot_amount"`
	MinimumSettlementAmountOut string `json:"minimum_settlement_amount_out"`
	PerpBaseAmount             uint64 `json:"perp_base_amount"`
}

type RunRequest struct {
	Evaluation   SourceEvaluation     `json:"evaluation"`
	Readiness    Readiness            `json:"readiness"`
	AccountState AccountState         `json:"account_state"`
	Quotes       protocol.QuoteBundle `json:"quotes"`
	OpenEpisode  *OpenEpisode         `json:"open_episode,omitempty"`
}

type FrozenEvidence struct {
	DatasetManifest          string `json:"dataset_manifest"`
	StrategyVersion          string `json:"strategy_version"`
	MarketManifest           string `json:"market_manifest"`
	QuoteBlockHash           string `json:"quote_block_hash"`
	QuoteReceivedAtMS        uint64 `json:"quote_received_at_ms"`
	QuoteExpiresAtMS         uint64 `json:"quote_expires_at_ms"`
	UIMultiplierE18          string `json:"ui_multiplier_e18"`
	PerpMarkPrice            uint32 `json:"perp_mark_price"`
	EstimatedTotalCostMicros uint64 `json:"estimated_total_cost_micros"`
}

type PairIntent struct {
	Version                    uint8          `json:"version"`
	ID                         string         `json:"id"`
	SpotUnwindIntentID         string         `json:"spot_unwind_intent_id"`
	ExecutionAccountID         string         `json:"execution_account_id"`
	AgentID                    string         `json:"agent_id"`
	SourceEvaluationID         string         `json:"source_evaluation_id"`
	RiskVersion                string         `json:"risk_version"`
	StrategyManifestSHA256     string         `json:"strategy_manifest_sha256"`
	LighterAccountIndex        uint64         `json:"lighter_account_index"`
	LighterAPIKeyIndex         uint8          `json:"lighter_api_key_index"`
	RobinhoodVault             string         `json:"robinhood_vault"`
	RobinhoodSigner            string         `json:"robinhood_signer"`
	Symbol                     string         `json:"symbol"`
	SpotToken                  string         `json:"spot_token"`
	LighterMarketIndex         uint32         `json:"lighter_market_index"`
	SpotSide                   string         `json:"spot_side"`
	PerpSide                   string         `json:"perp_side"`
	SpotNotionalMicros         uint64         `json:"spot_notional_micros"`
	PerpNotionalMicros         uint64         `json:"perp_notional_micros"`
	NAVMicros                  uint64         `json:"nav_micros"`
	RawSpotAmount              string         `json:"raw_spot_amount"`
	SettlementAmountIn         string         `json:"settlement_amount_in"`
	MinimumSpotAmountOut       string         `json:"minimum_spot_amount_out"`
	MinimumUnwindSettlementOut string         `json:"minimum_unwind_settlement_out"`
	SpotDecimals               uint8          `json:"spot_decimals"`
	SpotConfigVersion          uint64         `json:"spot_config_version"`
	PerpBaseAmount             uint64         `json:"perp_base_amount"`
	PerpBaseDecimals           uint8          `json:"perp_base_decimals"`
	PerpPriceDecimals          uint8          `json:"perp_price_decimals"`
	PerpLimitPrice             uint32         `json:"perp_limit_price"`
	ClientOrderIndex           uint64         `json:"client_order_index"`
	PerpUnwindPrice            uint32         `json:"perp_unwind_price"`
	UnwindClientOrderIndex     uint64         `json:"unwind_client_order_index"`
	MaxUnwindAttempts          uint8          `json:"max_unwind_attempts"`
	PerpOrderExpiryMS          uint64         `json:"perp_order_expiry_ms"`
	EmergencyDeadlineMS        uint64         `json:"emergency_deadline_ms"`
	ReconciliationDeadlineMS   uint64         `json:"reconciliation_deadline_ms"`
	LeverageMicros             uint64         `json:"leverage_micros"`
	CreatedAtMS                uint64         `json:"created_at_ms"`
	DeadlineMS                 uint64         `json:"deadline_ms"`
	Evidence                   FrozenEvidence `json:"evidence"`
}

type UnwindDirective struct {
	Version                    uint8  `json:"version"`
	ID                         string `json:"id"`
	PairIntentID               string `json:"pair_intent_id"`
	SpotUnwindIntentID         string `json:"spot_unwind_intent_id"`
	ExecutionAccountID         string `json:"execution_account_id"`
	AgentID                    string `json:"agent_id"`
	SourceEvaluationID         string `json:"source_evaluation_id"`
	StrategyVersion            string `json:"strategy_version"`
	StrategyManifestSHA256     string `json:"strategy_manifest_sha256"`
	RiskVersion                string `json:"risk_version"`
	SpotSide                   string `json:"spot_side"`
	SpotAmountIn               string `json:"spot_amount_in"`
	MinimumSettlementAmountOut string `json:"minimum_settlement_amount_out"`
	PerpSide                   string `json:"perp_side"`
	PerpBaseAmount             uint64 `json:"perp_base_amount"`
	PerpLimitPrice             uint32 `json:"perp_limit_price"`
	ReduceOnly                 bool   `json:"reduce_only"`
	QuoteSourceSession         string `json:"quote_source_session"`
	QuoteSourceEventID         string `json:"quote_source_event_id"`
	QuotePayloadSHA256         string `json:"quote_payload_sha256"`
	ObservedAtMS               uint64 `json:"observed_at_ms"`
	DeadlineMS                 uint64 `json:"deadline_ms"`
	ReconciliationDeadlineMS   uint64 `json:"reconciliation_deadline_ms"`
}

type RunOutput struct {
	Kind            protocol.Action    `json:"kind"`
	PairIntent      *PairIntent        `json:"pair_intent,omitempty"`
	Unwind          *UnwindDirective   `json:"unwind,omitempty"`
	Persistence     *IntentPersistence `json:"persistence,omitempty"`
	ExitPersistence *ExitPersistence   `json:"exit_persistence,omitempty"`
}
