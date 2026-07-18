package evaluation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	scheduler "github.com/robin-the-claw/live-scheduler"
)

type ApprovalStore interface {
	Approve(context.Context, PaperCandidate, ProductAccount, time.Time, time.Duration, uint64, uint32) (bool, error)
	ApproveExit(context.Context, PaperExit, ProductAccount, time.Time, time.Duration, uint32) (bool, error)
}

type PGStore struct {
	pool *pgxpool.Pool
}

type coordinatorState struct {
	AgentID             string
	StrategyVersion     string
	RiskVersion         string
	Status              string
	LighterAccountIndex uint64
	LighterAPIKeyIndex  uint8
	Owner               string
	Vault               string
	Signer              string
	BindingSHA256       string
	StrategyManifest    string
	AccountControl      string
	StrategyControl     string
	GlobalControl       string
	VenueApproved       bool
	OracleHealthy       bool
	SequencerHealthy    bool
	ReconciliationReady bool
	ExitAuthorityReady  bool
	ReadinessUpdatedAt  time.Time
	AlertingReady       bool
	SafeRotationReady   bool
	PromotionState      string
}

type lighterSnapshot struct {
	AccountIndex            uint64 `json:"account_index"`
	APIKeyIndex             uint8  `json:"api_key_index"`
	MarketIndex             uint32 `json:"market_index"`
	NonceAligned            bool   `json:"nonce_aligned"`
	NoUnknownOrders         bool   `json:"no_unknown_orders"`
	NoUnknownPositions      bool   `json:"no_unknown_positions"`
	CollateralReady         bool   `json:"collateral_ready"`
	MaintenanceMarginRatio  uint64 `json:"maintenance_margin_ratio_micros"`
	CollateralMicros        uint64 `json:"collateral_micros"`
	MaintenanceMarginMicros uint64 `json:"maintenance_margin_micros"`
	Flat                    bool   `json:"flat"`
}

type robinhoodSnapshot struct {
	VaultAddress          string `json:"vault_address"`
	SignerAddress         string `json:"signer_address"`
	FundingReady          bool   `json:"funding_ready"`
	WiringVerified        bool   `json:"wiring_verified"`
	FinalityHealthy       bool   `json:"finality_healthy"`
	Flat                  bool   `json:"flat"`
	OwnerAddress          string `json:"owner_address"`
	AgentEnabled          bool   `json:"agent_enabled"`
	FinalizedAgentAddress string `json:"finalized_agent_address"`
	FinalizedAgentEnabled bool   `json:"finalized_agent_enabled"`
	GlobalMode            string `json:"global_mode"`
	FinalizedGlobalMode   string `json:"finalized_global_mode"`
	RiskMode              string `json:"risk_mode"`
	FinalizedRiskMode     string `json:"finalized_risk_mode"`
	SettlementBalanceRaw  string `json:"settlement_balance_raw"`
	NonceAligned          bool   `json:"nonce_aligned"`
	SpotConfigVersion     uint64 `json:"spot_config_version"`
	StockDecimals         uint8  `json:"stock_decimals"`
	UIMultiplierE18       string `json:"ui_multiplier_e18"`
	NewUIMultiplierE18    string `json:"new_ui_multiplier_e18"`
	OraclePaused          bool   `json:"oracle_paused"`
	OracleHealthy         bool   `json:"oracle_healthy"`
	SequencerHealthy      bool   `json:"sequencer_healthy"`
	SignerGasReady        bool   `json:"signer_gas_ready"`
	FinalizedNumber       uint64 `json:"finalized_number"`
	FinalizedHash         string `json:"finalized_hash"`
	FinalizedTimestamp    uint64 `json:"finalized_timestamp"`
	SourceBlockNumber     uint64 `json:"source_block_number"`
	SourceBlockHash       string `json:"source_block_hash"`
	SourceBlockTimestamp  uint64 `json:"source_block_timestamp"`
}

type snapshots struct {
	Lighter           lighterSnapshot
	Robinhood         robinhoodSnapshot
	LighterObserved   time.Time
	RobinhoodObserved time.Time
	LighterExpires    time.Time
	RobinhoodExpires  time.Time
}

type episodeBinding struct {
	IntentID          string
	AgentID           string
	EntryEvaluationID string
	MarketManifest    string
	ClientOrderIndex  uint64
	UnwindOrderIndex  uint64
}

func NewWritePool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("parse execution database URL")
	}
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func NewPGStore(pool *pgxpool.Pool) (*PGStore, error) {
	if pool == nil {
		return nil, errors.New("execution pool is required")
	}
	return &PGStore{pool: pool}, nil
}

func (store *PGStore) Ready(ctx context.Context) error {
	for _, relation := range []string{
		"public.execution_accounts",
		"public.execution_account_registrations",
		"public.execution_account_readiness",
		"public.execution_rollout_readiness",
		"public.execution_market_configs",
		"public.execution_market_review_records",
		"public.execution_market_review_observations",
		"public.live_scheduler_approvals",
		"public.live_evaluation_order_cursors",
		"public.live_evaluation_order_allocations",
		"public.live_execution_episode_bindings",
		"public.live_strategy_exit_bindings",
	} {
		var value string
		if err := store.pool.QueryRow(ctx, "SELECT to_regclass($1)::text", relation).Scan(&value); err != nil || value == "" {
			return fmt.Errorf("required execution relation is unavailable: %s", relation)
		}
	}
	var versioned bool
	if err := store.pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = 'public' AND table_name = 'live_scheduler_approvals'
      AND column_name = 'approval_version'
)`).Scan(&versioned); err != nil || !versioned {
		return errors.New("live scheduler approval version is unavailable")
	}
	return nil
}

func (store *PGStore) Approve(
	ctx context.Context,
	candidate PaperCandidate,
	product ProductAccount,
	now time.Time,
	lifetime time.Duration,
	minimumNetEdgePPM uint64,
	lighterMarket uint32,
) (bool, error) {
	if err := candidate.Validate(now, minimumNetEdgePPM); err != nil {
		return false, err
	}
	if err := product.Validate(now); err != nil {
		return false, err
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", product.ExecutionAccountID); err != nil {
		return false, err
	}

	state, err := loadCoordinatorState(ctx, tx, product.ExecutionAccountID)
	if err != nil {
		return false, err
	}
	if err := verifyCoordinatorState(state, product, now); err != nil {
		return false, err
	}
	market, err := loadMarket(ctx, tx, now)
	if err != nil {
		return false, err
	}
	if err := validateMarket(market, now, lighterMarket); err != nil {
		return false, err
	}
	observations, err := loadSnapshots(ctx, tx, product.ExecutionAccountID, now)
	if err != nil {
		return false, err
	}
	if err := verifySnapshots(observations, state, market, now); err != nil {
		return false, err
	}
	var dailyTurnover uint64
	var activeEpisodes uint8
	if err := loadExposure(ctx, tx, product.ExecutionAccountID, now, &dailyTurnover, &activeEpisodes); err != nil {
		return false, err
	}
	if dailyTurnover > dailyTurnoverMicros-2*entryNotionalMicros || activeEpisodes != 0 {
		return false, errors.New("account exposure limits are unavailable")
	}

	datasetManifest, err := DatasetManifest(candidate)
	if err != nil {
		return false, err
	}
	evaluationID, err := SourceEvaluationID(candidate, datasetManifest, market.ManifestID)
	if err != nil {
		return false, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM live_scheduler_approvals
    WHERE evaluation_id = $1 AND execution_account_id = $2
)`, evaluationID, product.ExecutionAccountID).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, tx.Commit(ctx)
	}

	clientIndex, unwindIndex, err := reserveOrderIndices(ctx, tx, product.ExecutionAccountID)
	if err != nil {
		return false, err
	}
	approval, err := buildApproval(candidate, product, state, market, observations, dailyTurnover,
		activeEpisodes, datasetManifest, evaluationID, now, lifetime, clientIndex, unwindIndex)
	if err != nil {
		return false, err
	}
	evaluationJSON, err := json.Marshal(approval.Evaluation)
	if err != nil {
		return false, err
	}
	readinessJSON, err := json.Marshal(approval.Readiness)
	if err != nil {
		return false, err
	}
	stateJSON, err := json.Marshal(approval.State)
	if err != nil {
		return false, err
	}
	approvalSHA, err := scheduler.ApprovalSHA256(product.ExecutionAccountID, product.AgentID,
		approval.Evaluation, approval.Readiness, approval.State, approval.ExpiresAt)
	if err != nil {
		return false, err
	}
	command, err := tx.Exec(ctx, `
INSERT INTO live_scheduler_approvals (
    evaluation_id, execution_account_id, agent_id, evaluation, readiness,
    account_state, approval_sha256, approved_at, expires_at
) VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb, $7, $8, $9)
ON CONFLICT (evaluation_id, execution_account_id) DO NOTHING`,
		approval.Evaluation.ID, product.ExecutionAccountID, product.AgentID,
		string(evaluationJSON), string(readinessJSON), string(stateJSON), approvalSHA, now, approval.ExpiresAt)
	if err != nil {
		return false, fmt.Errorf("insert scheduler approval: %w", err)
	}
	if command.RowsAffected() != 1 {
		return false, errors.New("scheduler approval changed concurrently")
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO live_evaluation_order_allocations (
    evaluation_id, execution_account_id, client_order_index,
    unwind_order_index, unwind_order_count, allocated_at
) VALUES ($1, $2, $3, $4, 3, $5)`, approval.Evaluation.ID, product.ExecutionAccountID,
		clientIndex, unwindIndex, now); err != nil {
		return false, fmt.Errorf("record live order allocation: %w", err)
	}
	return true, tx.Commit(ctx)
}

func (store *PGStore) ApproveExit(
	ctx context.Context,
	exit PaperExit,
	product ProductAccount,
	now time.Time,
	lifetime time.Duration,
	lighterMarket uint32,
) (bool, error) {
	if err := exit.Validate(now); err != nil {
		return false, err
	}
	if err := product.Validate(now); err != nil {
		return false, err
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", product.ExecutionAccountID); err != nil {
		return false, err
	}

	binding, err := loadEpisodeBinding(ctx, tx, product.ExecutionAccountID, exit.EpisodeID)
	if err != nil || binding == nil {
		return false, err
	}
	if binding.AgentID != product.AgentID {
		return false, errors.New("paper episode is bound to a different agent")
	}
	state, err := loadCoordinatorState(ctx, tx, product.ExecutionAccountID)
	if err != nil {
		return false, err
	}
	if err := verifyCoordinatorStateFor(state, product, now, scheduler.ActionUnwind); err != nil {
		return false, err
	}
	market, err := loadMarket(ctx, tx, now)
	if err != nil {
		return false, err
	}
	if err := validateMarket(market, now, lighterMarket); err != nil {
		return false, err
	}
	if binding.MarketManifest != market.ManifestID {
		return false, errors.New("open episode market manifest changed")
	}
	observations, err := loadSnapshots(ctx, tx, product.ExecutionAccountID, now)
	if err != nil {
		return false, err
	}
	if err := verifyExitSnapshots(observations, state, market, now); err != nil {
		return false, err
	}
	var dailyTurnover uint64
	var activeEpisodes uint8
	if err := loadExposure(ctx, tx, product.ExecutionAccountID, now, &dailyTurnover, &activeEpisodes); err != nil {
		return false, err
	}
	if activeEpisodes != 1 {
		return false, errors.New("natural exit requires exactly one active execution episode")
	}

	datasetManifest, err := ExitDatasetManifest(exit)
	if err != nil {
		return false, err
	}
	evaluationID, err := ExitEvaluationID(exit, datasetManifest, market.ManifestID, binding.IntentID)
	if err != nil {
		return false, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM live_scheduler_approvals
    WHERE evaluation_id = $1 AND execution_account_id = $2
)`, evaluationID, product.ExecutionAccountID).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, tx.Commit(ctx)
	}

	approval, err := buildExitApproval(exit, product, state, market, observations,
		dailyTurnover, datasetManifest, evaluationID, binding, now, lifetime)
	if err != nil {
		return false, err
	}
	evaluationJSON, err := json.Marshal(approval.Evaluation)
	if err != nil {
		return false, err
	}
	readinessJSON, err := json.Marshal(approval.Readiness)
	if err != nil {
		return false, err
	}
	stateJSON, err := json.Marshal(approval.State)
	if err != nil {
		return false, err
	}
	approvalSHA, err := scheduler.ApprovalSHA256(product.ExecutionAccountID, product.AgentID,
		approval.Evaluation, approval.Readiness, approval.State, approval.ExpiresAt)
	if err != nil {
		return false, err
	}
	command, err := tx.Exec(ctx, `
INSERT INTO live_scheduler_approvals (
    evaluation_id, execution_account_id, agent_id, evaluation, readiness,
    account_state, approval_sha256, approved_at, expires_at
) VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb, $7, $8, $9)
ON CONFLICT (evaluation_id, execution_account_id) DO NOTHING`,
		approval.Evaluation.ID, product.ExecutionAccountID, product.AgentID,
		string(evaluationJSON), string(readinessJSON), string(stateJSON), approvalSHA, now, approval.ExpiresAt)
	if err != nil {
		return false, fmt.Errorf("insert scheduler exit approval: %w", err)
	}
	if command.RowsAffected() != 1 {
		return false, errors.New("scheduler exit approval changed concurrently")
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO live_strategy_exit_bindings (
    exit_evaluation_id, execution_account_id, intent_id, source_episode_id,
    source_close_evaluation_id, close_reason, approved_at
) VALUES ($1, $2, $3, $4::uuid, $5::uuid, $6, $7)`, evaluationID,
		product.ExecutionAccountID, binding.IntentID, exit.EpisodeID, exit.EvaluationID,
		exit.Reason, now); err != nil {
		return false, fmt.Errorf("bind natural strategy exit: %w", err)
	}
	return true, tx.Commit(ctx)
}

func loadCoordinatorState(ctx context.Context, tx pgx.Tx, accountID string) (coordinatorState, error) {
	var state coordinatorState
	var accountIndex int64
	var apiKey int16
	err := tx.QueryRow(ctx, `
SELECT account.agent_id, account.strategy_version, account.risk_version, account.status,
       registration.lighter_account_index, registration.lighter_api_key_index,
       lower(registration.robinhood_owner), lower(registration.robinhood_vault),
       lower(registration.robinhood_signer), registration.binding_sha256,
       registration.strategy_manifest_sha256, account_control.mode,
       strategy_control.mode, global_control.mode,
       readiness.venue_approved, readiness.oracle_healthy, readiness.sequencer_healthy,
       readiness.reconciliation_ready, readiness.exit_authority_ready, readiness.updated_at,
       rollout.alerting_ready, rollout.safe_rotation_ready, promotion.to_state
FROM execution_accounts account
JOIN execution_account_registrations registration USING (execution_account_id)
JOIN execution_account_control account_control USING (execution_account_id)
JOIN execution_account_readiness readiness USING (execution_account_id)
JOIN execution_strategy_control strategy_control
  ON strategy_control.strategy_version = account.strategy_version
CROSS JOIN execution_control global_control
CROSS JOIN execution_rollout_readiness rollout
CROSS JOIN LATERAL (
    SELECT to_state FROM execution_promotion_events
    WHERE strategy_version = account.strategy_version
    ORDER BY id DESC LIMIT 1
) promotion
WHERE account.execution_account_id = $1
  AND global_control.singleton
  AND rollout.singleton
  AND account.agent_id = registration.agent_id
  AND account.strategy_version = registration.strategy_version
  AND account.risk_version = registration.risk_version
  AND account.strategy_manifest_sha256 = registration.strategy_manifest_sha256
  AND account.lighter_account_index = registration.lighter_account_index
  AND account.lighter_api_key_index = registration.lighter_api_key_index
  AND account.owner_address = registration.robinhood_owner
  AND account.robinhood_vault = registration.robinhood_vault
  AND account.robinhood_signer = registration.robinhood_signer
  AND account.binding_sha256 = registration.binding_sha256
  AND strategy_control.strategy_manifest_sha256 = account.strategy_manifest_sha256
FOR SHARE OF account, registration, account_control, readiness, strategy_control,
             global_control, rollout`, accountID).Scan(
		&state.AgentID, &state.StrategyVersion, &state.RiskVersion, &state.Status,
		&accountIndex, &apiKey, &state.Owner, &state.Vault, &state.Signer,
		&state.BindingSHA256, &state.StrategyManifest, &state.AccountControl,
		&state.StrategyControl, &state.GlobalControl, &state.VenueApproved,
		&state.OracleHealthy, &state.SequencerHealthy, &state.ReconciliationReady,
		&state.ExitAuthorityReady, &state.ReadinessUpdatedAt, &state.AlertingReady,
		&state.SafeRotationReady, &state.PromotionState)
	if err != nil {
		return coordinatorState{}, fmt.Errorf("load coordinator state: %w", err)
	}
	if accountIndex <= 0 || apiKey < 0 || apiKey > 255 {
		return coordinatorState{}, errors.New("coordinator account index is invalid")
	}
	state.LighterAccountIndex = uint64(accountIndex)
	state.LighterAPIKeyIndex = uint8(apiKey)
	return state, nil
}

func loadEpisodeBinding(ctx context.Context, tx pgx.Tx, accountID, sourceEpisodeID string) (*episodeBinding, error) {
	row := tx.QueryRow(ctx, `
SELECT binding.intent_id, binding.agent_id, binding.entry_evaluation_id,
       binding.market_manifest, allocation.client_order_index,
       allocation.unwind_order_index
FROM live_execution_episode_bindings binding
JOIN execution_intents intent
  ON intent.id = binding.intent_id
 AND intent.execution_account_id = binding.execution_account_id
JOIN live_evaluation_order_allocations allocation
  ON allocation.evaluation_id = binding.entry_evaluation_id
 AND allocation.execution_account_id = binding.execution_account_id
WHERE binding.execution_account_id = $1
  AND binding.source_episode_id = $2::uuid
  AND intent.active
FOR SHARE OF binding, intent, allocation`, accountID, sourceEpisodeID)
	var binding episodeBinding
	var clientIndex, unwindIndex int64
	if err := row.Scan(&binding.IntentID, &binding.AgentID, &binding.EntryEvaluationID,
		&binding.MarketManifest, &clientIndex, &unwindIndex); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if !hashPattern.MatchString(binding.IntentID) || !hashPattern.MatchString(binding.EntryEvaluationID) ||
		clientIndex <= 0 || unwindIndex <= 0 || uint64(clientIndex) > maximumLiveOrderIndex ||
		uint64(unwindIndex) > maximumLiveOrderIndex {
		return nil, errors.New("live episode binding is invalid")
	}
	binding.ClientOrderIndex = uint64(clientIndex)
	binding.UnwindOrderIndex = uint64(unwindIndex)
	return &binding, nil
}

func verifyCoordinatorState(state coordinatorState, product ProductAccount, now time.Time) error {
	return verifyCoordinatorStateFor(state, product, now, scheduler.ActionEntry)
}

func verifyCoordinatorStateFor(state coordinatorState, product ProductAccount, now time.Time, action string) error {
	controlsReady := state.AccountControl == "ACTIVE" && state.StrategyControl == "ACTIVE" && state.GlobalControl == "ACTIVE"
	if action == scheduler.ActionUnwind {
		controlsReady = (state.AccountControl == "ACTIVE" || state.AccountControl == "REDUCE_ONLY") &&
			(state.StrategyControl == "ACTIVE" || state.StrategyControl == "REDUCE_ONLY") &&
			(state.GlobalControl == "ACTIVE" || state.GlobalControl == "REDUCE_ONLY")
	}
	if state.AgentID != product.AgentID || state.StrategyVersion != scheduler.StrategyVersion ||
		state.RiskVersion != scheduler.StrategyVersion || state.Status != "active" ||
		state.LighterAccountIndex != product.LighterAccount || state.LighterAPIKeyIndex != product.LighterAPIKey ||
		state.Owner != product.RobinhoodOwner || state.Vault != product.RobinhoodVault ||
		state.Signer != product.RobinhoodSigner || state.BindingSHA256 != product.BindingSHA256 ||
		state.StrategyManifest != scheduler.StrategyManifestSHA256 || !controlsReady || state.PromotionState != "canary_eligible" ||
		stale(state.ReadinessUpdatedAt, now) || !state.VenueApproved || !state.OracleHealthy ||
		!state.SequencerHealthy || !state.ReconciliationReady || !state.ExitAuthorityReady ||
		!state.AlertingReady || !state.SafeRotationReady {
		return errors.New("coordinator account is not live-ready")
	}
	return nil
}

func loadMarket(ctx context.Context, tx pgx.Tx, now time.Time) (MarketConfig, error) {
	rows, err := tx.Query(ctx, `
SELECT manifest_id, symbol, lower(spot_token), lighter_market_index, spot_decimals,
       perp_base_decimals, perp_price_decimals, spot_config_version, ui_multiplier_e18,
       max_price_deviation_bps, max_spot_slippage_bps,
       max_unwind_price_deviation_bps, review_record_sha256, valid_from, valid_until
FROM execution_market_configs
WHERE symbol = $1 AND valid_from <= $2 AND valid_until > $2
ORDER BY manifest_id
FOR SHARE`, Symbol, now)
	if err != nil {
		return MarketConfig{}, err
	}
	defer rows.Close()
	var configs []MarketConfig
	for rows.Next() {
		var config MarketConfig
		var market, spotDecimals, baseDecimals, priceDecimals int32
		var configVersion int64
		var priceDeviation, spotSlippage, unwindDeviation int32
		if err := rows.Scan(&config.ManifestID, &config.Symbol, &config.SpotToken, &market,
			&spotDecimals, &baseDecimals, &priceDecimals, &configVersion, &config.UIMultiplierE18,
			&priceDeviation, &spotSlippage, &unwindDeviation, &config.ReviewRecordSHA256,
			&config.ValidFrom, &config.ValidUntil); err != nil {
			return MarketConfig{}, err
		}
		if market < 0 || spotDecimals < 0 || baseDecimals < 0 || priceDecimals < 0 || configVersion <= 0 ||
			priceDeviation < 0 || spotSlippage < 0 || unwindDeviation < 0 {
			return MarketConfig{}, errors.New("market config contains a negative value")
		}
		config.LighterMarketIndex = uint32(market)
		config.SpotDecimals = uint8(spotDecimals)
		config.PerpBaseDecimals = uint8(baseDecimals)
		config.PerpPriceDecimals = uint8(priceDecimals)
		config.SpotConfigVersion = uint64(configVersion)
		config.MaxPriceDeviationBPS = uint16(priceDeviation)
		config.MaxSpotSlippageBPS = uint16(spotSlippage)
		config.MaxUnwindPriceDeviationBPS = uint16(unwindDeviation)
		configs = append(configs, config)
	}
	if err := rows.Err(); err != nil {
		return MarketConfig{}, err
	}
	if len(configs) != 1 {
		return MarketConfig{}, errors.New("exactly one current AAPL market config is required")
	}
	return configs[0], nil
}

func loadSnapshots(ctx context.Context, tx pgx.Tx, accountID string, now time.Time) (snapshots, error) {
	rows, err := tx.Query(ctx, `
SELECT DISTINCT ON (source) source, payload, observed_at, expires_at
FROM execution_account_snapshots
WHERE execution_account_id = $1
  AND observed_at <= $2
  AND received_at <= $2
  AND received_at >= $2 - interval '5 seconds'
  AND expires_at > $2
ORDER BY source, received_at DESC, id DESC
`, accountID, now)
	if err != nil {
		return snapshots{}, err
	}
	defer rows.Close()
	var result snapshots
	var lighterFound, robinhoodFound bool
	for rows.Next() {
		var source string
		var body []byte
		var observed, expires time.Time
		if err := rows.Scan(&source, &body, &observed, &expires); err != nil {
			return snapshots{}, err
		}
		switch source {
		case "lighter-auth":
			if err := decodeStrict(body, &result.Lighter); err != nil {
				return snapshots{}, fmt.Errorf("decode Lighter snapshot: %w", err)
			}
			result.LighterObserved, result.LighterExpires, lighterFound = observed, expires, true
		case "robinhood-chain":
			if err := decodeStrict(body, &result.Robinhood); err != nil {
				return snapshots{}, fmt.Errorf("decode Robinhood snapshot: %w", err)
			}
			result.RobinhoodObserved, result.RobinhoodExpires, robinhoodFound = observed, expires, true
		}
	}
	if err := rows.Err(); err != nil {
		return snapshots{}, err
	}
	if !lighterFound || !robinhoodFound {
		return snapshots{}, errors.New("both authenticated account snapshots are required")
	}
	return result, nil
}

func verifySnapshots(value snapshots, state coordinatorState, market MarketConfig, now time.Time) error {
	lighter, robinhood := value.Lighter, value.Robinhood
	if stale(value.LighterObserved, now) || stale(value.RobinhoodObserved, now) ||
		lighter.AccountIndex != state.LighterAccountIndex || lighter.APIKeyIndex != state.LighterAPIKeyIndex ||
		lighter.MarketIndex != market.LighterMarketIndex || !lighter.NonceAligned || !lighter.NoUnknownOrders ||
		!lighter.NoUnknownPositions || !lighter.CollateralReady || !lighter.Flat || lighter.CollateralMicros == 0 {
		return errors.New("Lighter snapshot is not entry-safe")
	}
	if lighter.MaintenanceMarginMicros > 0 {
		ratio := new(big.Int).Mul(new(big.Int).SetUint64(lighter.CollateralMicros), big.NewInt(1_000_000))
		ratio.Quo(ratio, new(big.Int).SetUint64(lighter.MaintenanceMarginMicros))
		if !ratio.IsUint64() || ratio.Uint64() != lighter.MaintenanceMarginRatio || lighter.CollateralMicros/2 < lighter.MaintenanceMarginMicros {
			return errors.New("Lighter margin evidence is inconsistent")
		}
	} else if lighter.MaintenanceMarginRatio != 10_000_000 {
		return errors.New("zero-maintenance Lighter margin evidence is inconsistent")
	}
	if !robinhoodSourceBound(robinhood, value.RobinhoodObserved) ||
		robinhood.OwnerAddress != state.Owner || robinhood.VaultAddress != state.Vault || robinhood.SignerAddress != state.Signer ||
		!robinhood.FundingReady || !robinhood.WiringVerified || !robinhood.FinalityHealthy || !robinhood.Flat ||
		!robinhood.AgentEnabled || !robinhood.FinalizedAgentEnabled ||
		robinhood.FinalizedAgentAddress != state.Signer ||
		robinhood.GlobalMode != "ACTIVE" || robinhood.FinalizedGlobalMode != "ACTIVE" ||
		robinhood.RiskMode != "ACTIVE" || robinhood.FinalizedRiskMode != "ACTIVE" || !robinhood.NonceAligned ||
		robinhood.SpotConfigVersion != market.SpotConfigVersion || robinhood.StockDecimals != market.SpotDecimals ||
		robinhood.UIMultiplierE18 != market.UIMultiplierE18 || robinhood.NewUIMultiplierE18 != market.UIMultiplierE18 ||
		robinhood.OraclePaused || !robinhood.OracleHealthy || !robinhood.SequencerHealthy || !robinhood.SignerGasReady {
		return errors.New("Robinhood snapshot is not entry-safe")
	}
	settlement, err := strconv.ParseUint(robinhood.SettlementBalanceRaw, 10, 64)
	if err != nil || settlement < entryNotionalMicros {
		return errors.New("Robinhood settlement balance is insufficient")
	}
	return nil
}

func verifyExitSnapshots(value snapshots, state coordinatorState, market MarketConfig, now time.Time) error {
	lighter, robinhood := value.Lighter, value.Robinhood
	if stale(value.LighterObserved, now) || stale(value.RobinhoodObserved, now) ||
		lighter.AccountIndex != state.LighterAccountIndex || lighter.APIKeyIndex != state.LighterAPIKeyIndex ||
		lighter.MarketIndex != market.LighterMarketIndex || !lighter.NonceAligned || !lighter.NoUnknownOrders ||
		!lighter.NoUnknownPositions || !lighter.CollateralReady || lighter.Flat || lighter.CollateralMicros == 0 {
		return errors.New("Lighter snapshot is not exit-safe")
	}
	if !robinhoodSourceBound(robinhood, value.RobinhoodObserved) ||
		robinhood.OwnerAddress != state.Owner || robinhood.VaultAddress != state.Vault || robinhood.SignerAddress != state.Signer ||
		!robinhood.WiringVerified || !robinhood.FinalityHealthy || robinhood.Flat || !robinhood.AgentEnabled ||
		(robinhood.RiskMode != "ACTIVE" && robinhood.RiskMode != "REDUCE_ONLY") || !robinhood.NonceAligned ||
		robinhood.SpotConfigVersion != market.SpotConfigVersion || robinhood.StockDecimals != market.SpotDecimals ||
		robinhood.UIMultiplierE18 != market.UIMultiplierE18 || robinhood.NewUIMultiplierE18 != market.UIMultiplierE18 ||
		robinhood.OraclePaused || !robinhood.OracleHealthy || !robinhood.SequencerHealthy || !robinhood.SignerGasReady {
		return errors.New("Robinhood snapshot is not exit-safe")
	}
	return nil
}

func robinhoodSourceBound(snapshot robinhoodSnapshot, observed time.Time) bool {
	if observed.IsZero() || observed.Unix() < 0 || snapshot.FinalizedNumber == 0 ||
		!validHash(snapshot.FinalizedHash) || snapshot.FinalizedTimestamp == 0 ||
		snapshot.SourceBlockNumber < snapshot.FinalizedNumber || !validHash(snapshot.SourceBlockHash) ||
		snapshot.SourceBlockTimestamp < snapshot.FinalizedTimestamp ||
		snapshot.SourceBlockTimestamp-snapshot.FinalizedTimestamp > 30*60 ||
		uint64(observed.Unix()) != snapshot.SourceBlockTimestamp ||
		!observed.Equal(time.Unix(int64(snapshot.SourceBlockTimestamp), 0)) {
		return false
	}
	return snapshot.SourceBlockNumber != snapshot.FinalizedNumber ||
		snapshot.SourceBlockHash == snapshot.FinalizedHash
}

func loadExposure(ctx context.Context, tx pgx.Tx, accountID string, now time.Time, turnover *uint64, active *uint8) error {
	var daily int64
	var episodes int64
	err := tx.QueryRow(ctx, `
SELECT coalesce((
           SELECT entry_gross_micros FROM execution_account_daily_turnover
           WHERE execution_account_id = $1 AND trading_day = ($2 AT TIME ZONE 'UTC')::date
       ), 0),
       (SELECT count(*) FROM execution_intents WHERE execution_account_id = $1 AND active)`,
		accountID, now).Scan(&daily, &episodes)
	if err != nil {
		return err
	}
	if daily < 0 || episodes < 0 || episodes > 255 {
		return errors.New("account exposure evidence is invalid")
	}
	*turnover, *active = uint64(daily), uint8(episodes)
	return nil
}

func reserveOrderIndices(ctx context.Context, tx pgx.Tx, accountID string) (uint64, uint64, error) {
	if _, err := tx.Exec(ctx, `
INSERT INTO live_evaluation_order_cursors (execution_account_id, next_order_index)
VALUES ($1, 1) ON CONFLICT (execution_account_id) DO NOTHING`, accountID); err != nil {
		return 0, 0, err
	}
	var next, legacyMax int64
	if err := tx.QueryRow(ctx, `
SELECT cursor.next_order_index,
       coalesce((
           SELECT max(parsed) FROM (
               SELECT CASE
                   WHEN value ~ '^[0-9]{1,13}$' THEN value::bigint
               END AS parsed
               FROM execution_identifiers
               WHERE namespace = 'lighter_client_order'
           ) identifiers
           WHERE parsed < 1099511627776
       ), 0)
FROM live_evaluation_order_cursors cursor
WHERE cursor.execution_account_id = $1
FOR UPDATE`, accountID).Scan(&next, &legacyMax); err != nil {
		return 0, 0, err
	}
	if legacyMax >= next {
		next = legacyMax + 1
	}
	if next <= 0 || uint64(next) > maximumLiveOrderIndex-3 {
		return 0, 0, errors.New("live order index range is exhausted")
	}
	if _, err := tx.Exec(ctx, `
UPDATE live_evaluation_order_cursors
SET next_order_index = $2, version = version + 1, updated_at = now()
WHERE execution_account_id = $1`, accountID, next+4); err != nil {
		return 0, 0, err
	}
	return uint64(next), uint64(next + 1), nil
}

func buildApproval(
	candidate PaperCandidate,
	product ProductAccount,
	state coordinatorState,
	market MarketConfig,
	observations snapshots,
	dailyTurnover uint64,
	activeEpisodes uint8,
	datasetManifest string,
	evaluationID string,
	now time.Time,
	lifetime time.Duration,
	clientIndex, unwindIndex uint64,
) (Approval, error) {
	cost, err := EstimatedCostMicros(candidate)
	if err != nil {
		return Approval{}, err
	}
	settlement, err := strconv.ParseUint(observations.Robinhood.SettlementBalanceRaw, 10, 64)
	if err != nil || settlement > ^uint64(0)-observations.Lighter.CollateralMicros {
		return Approval{}, errors.New("account NAV is not representable")
	}
	expiresAt := earliest(
		now.Add(lifetime), candidate.EvaluatedAt.Add(5*time.Second), product.ValidUntil,
		market.ValidUntil, observations.LighterExpires, observations.RobinhoodExpires,
	)
	if !expiresAt.After(now.Add(250 * time.Millisecond)) {
		return Approval{}, errors.New("approval evidence expires too soon")
	}
	readinessObserved := earliest(product.ObservedAt, state.ReadinessUpdatedAt,
		observations.LighterObserved, observations.RobinhoodObserved)
	accountObserved := earliest(observations.LighterObserved, observations.RobinhoodObserved)
	return Approval{
		Evaluation: scheduler.SourceEvaluation{
			ID: evaluationID, StrategyVersion: scheduler.StrategyVersion,
			StrategyManifestSHA256: scheduler.StrategyManifestSHA256,
			SourceConfigSHA256:     scheduler.SourceConfigSHA256,
			DatasetManifest:        datasetManifest, MarketManifest: market.ManifestID,
			Status: "approved", Action: scheduler.ActionEntry,
			ObservedAtMS: uint64(candidate.EvaluatedAt.UnixMilli()), EstimatedCostMicros: cost,
			SourceEpisodeID: candidate.EpisodeID, PaperEvaluationID: candidate.EvaluationID,
		},
		Readiness: scheduler.Readiness{
			ExecutionAccountID: product.ExecutionAccountID, AgentID: product.AgentID,
			StrategyVersion: scheduler.StrategyVersion, StrategyManifestSHA256: scheduler.StrategyManifestSHA256,
			Lifecycle: product.Lifecycle, GlobalControl: state.GlobalControl,
			StrategyControl: state.StrategyControl, AccountControl: state.AccountControl,
			FullyVerified: true, VaultWired: observations.Robinhood.WiringVerified,
			VaultFunded: observations.Robinhood.FundingReady, ExecutionSignerFunded: observations.Robinhood.SignerGasReady,
			LighterLinked: product.LighterLinked, LighterFunded: observations.Lighter.CollateralReady,
			RouteHealthy: true, OracleHealthy: observations.Robinhood.OracleHealthy,
			SequencerHealthy: observations.Robinhood.SequencerHealthy,
			ObservedAtMS:     uint64(readinessObserved.UnixMilli()),
		},
		State: scheduler.AccountState{
			ExecutionAccountID: product.ExecutionAccountID, AgentID: product.AgentID,
			StrategyManifestSHA256:   scheduler.StrategyManifestSHA256,
			LighterAccountIndex:      observations.Lighter.AccountIndex,
			LighterAPIKeyIndex:       observations.Lighter.APIKeyIndex,
			LighterMarketIndex:       observations.Lighter.MarketIndex,
			LighterNonceAligned:      observations.Lighter.NonceAligned,
			UnknownLighterOrders:     !observations.Lighter.NoUnknownOrders,
			UnknownLighterPositions:  !observations.Lighter.NoUnknownPositions,
			CollateralMicros:         observations.Lighter.CollateralMicros,
			MaintenanceMarginMicros:  observations.Lighter.MaintenanceMarginMicros,
			RobinhoodVault:           observations.Robinhood.VaultAddress,
			RobinhoodSigner:          observations.Robinhood.SignerAddress,
			RobinhoodNonceAligned:    observations.Robinhood.NonceAligned,
			UnknownRobinhoodPosition: !observations.Robinhood.Flat,
			NAVMicros:                settlement + observations.Lighter.CollateralMicros,
			DailyTurnoverMicros:      dailyTurnover, ActiveEpisodes: activeEpisodes,
			Flat:         observations.Lighter.Flat && observations.Robinhood.Flat,
			SpotDecimals: market.SpotDecimals, SpotConfigVersion: market.SpotConfigVersion,
			UIMultiplierE18: market.UIMultiplierE18, NextClientOrderIndex: clientIndex,
			NextUnwindOrderIndex: unwindIndex, ObservedAtMS: uint64(accountObserved.UnixMilli()),
		},
		ExpiresAt: expiresAt,
	}, nil
}

func buildExitApproval(
	exit PaperExit,
	product ProductAccount,
	state coordinatorState,
	market MarketConfig,
	observations snapshots,
	dailyTurnover uint64,
	datasetManifest, evaluationID string,
	binding *episodeBinding,
	now time.Time,
	lifetime time.Duration,
) (Approval, error) {
	settlement, err := strconv.ParseUint(observations.Robinhood.SettlementBalanceRaw, 10, 64)
	if err != nil || settlement > ^uint64(0)-observations.Lighter.CollateralMicros {
		return Approval{}, errors.New("account NAV is not representable")
	}
	expiresAt := earliest(now.Add(lifetime), exit.EvaluatedAt.Add(5*time.Second), product.ValidUntil,
		market.ValidUntil, observations.LighterExpires, observations.RobinhoodExpires)
	if !expiresAt.After(now.Add(250 * time.Millisecond)) {
		return Approval{}, errors.New("exit approval evidence expires too soon")
	}
	readinessObserved := earliest(product.ObservedAt, state.ReadinessUpdatedAt,
		observations.LighterObserved, observations.RobinhoodObserved)
	accountObserved := earliest(observations.LighterObserved, observations.RobinhoodObserved)
	return Approval{
		Evaluation: scheduler.SourceEvaluation{
			ID: evaluationID, StrategyVersion: scheduler.StrategyVersion,
			StrategyManifestSHA256: scheduler.StrategyManifestSHA256,
			SourceConfigSHA256:     scheduler.SourceConfigSHA256,
			DatasetManifest:        datasetManifest, MarketManifest: market.ManifestID,
			Status: "approved", Action: scheduler.ActionUnwind,
			ObservedAtMS: uint64(exit.EvaluatedAt.UnixMilli()), EstimatedCostMicros: 0,
			SourceEpisodeID: exit.EpisodeID, PaperEvaluationID: exit.EvaluationID,
			PairIntentID: binding.IntentID,
		},
		Readiness: scheduler.Readiness{
			ExecutionAccountID: product.ExecutionAccountID, AgentID: product.AgentID,
			StrategyVersion: scheduler.StrategyVersion, StrategyManifestSHA256: scheduler.StrategyManifestSHA256,
			Lifecycle: product.Lifecycle, GlobalControl: state.GlobalControl,
			StrategyControl: state.StrategyControl, AccountControl: state.AccountControl,
			FullyVerified: true, VaultWired: observations.Robinhood.WiringVerified,
			VaultFunded: true, ExecutionSignerFunded: observations.Robinhood.SignerGasReady,
			LighterLinked: product.LighterLinked, LighterFunded: observations.Lighter.CollateralReady,
			RouteHealthy: true, OracleHealthy: observations.Robinhood.OracleHealthy,
			SequencerHealthy: observations.Robinhood.SequencerHealthy,
			ObservedAtMS:     uint64(readinessObserved.UnixMilli()),
		},
		State: scheduler.AccountState{
			ExecutionAccountID: product.ExecutionAccountID, AgentID: product.AgentID,
			StrategyManifestSHA256:   scheduler.StrategyManifestSHA256,
			LighterAccountIndex:      observations.Lighter.AccountIndex,
			LighterAPIKeyIndex:       observations.Lighter.APIKeyIndex,
			LighterMarketIndex:       observations.Lighter.MarketIndex,
			LighterNonceAligned:      observations.Lighter.NonceAligned,
			UnknownLighterOrders:     !observations.Lighter.NoUnknownOrders,
			UnknownLighterPositions:  !observations.Lighter.NoUnknownPositions,
			CollateralMicros:         observations.Lighter.CollateralMicros,
			MaintenanceMarginMicros:  observations.Lighter.MaintenanceMarginMicros,
			RobinhoodVault:           observations.Robinhood.VaultAddress,
			RobinhoodSigner:          observations.Robinhood.SignerAddress,
			RobinhoodNonceAligned:    observations.Robinhood.NonceAligned,
			UnknownRobinhoodPosition: false,
			NAVMicros:                settlement + observations.Lighter.CollateralMicros,
			DailyTurnoverMicros:      dailyTurnover, ActiveEpisodes: 1, Flat: false,
			SpotDecimals: market.SpotDecimals, SpotConfigVersion: market.SpotConfigVersion,
			UIMultiplierE18:      market.UIMultiplierE18,
			NextClientOrderIndex: binding.ClientOrderIndex,
			NextUnwindOrderIndex: binding.UnwindOrderIndex,
			ObservedAtMS:         uint64(accountObserved.UnixMilli()),
		},
		ExpiresAt: expiresAt,
	}, nil
}

func decodeStrict(body []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func earliest(values ...time.Time) time.Time {
	result := values[0]
	for _, value := range values[1:] {
		if value.Before(result) {
			result = value
		}
	}
	return result
}
