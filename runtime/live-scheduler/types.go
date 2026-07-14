package scheduler

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"
)

const (
	StrategyVersion        = "basis-aapl-v1"
	StrategyManifestSHA256 = "da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f"
	SourceConfigSHA256     = "59106a18758a95af45e6ac1a8257843cfbd2a45fd09b5b3c3f429d3dedb56c2a"
	RouteSHA256            = routeSHA256
	OraclePolicySHA256     = oraclePolicySHA256
	RiskPolicySHA256       = riskPolicySHA256
	ActionEntry            = "entry"
	ActionUnwind           = "unwind"
	maxEvidenceAge         = 5 * time.Second
	quoteSchemaVersion     = uint8(3)
	routeSHA256            = "77d59f5e80e76ed507522b27ee6b7ddd1f8395f0337f0b230c5bba64bb335590"
	oraclePolicySHA256     = "7f0d306267da767869c0bc5951ce911ac1cb9060294edfa8eeefa884e0ddf937"
	riskPolicySHA256       = "a34ebfd2104eb062c9102f6fe68ad74d09a6e02f5c626fd17314228ae9bc398d"
)

var (
	hashPattern    = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
	accountPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,63}$`)
	uuidPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type SourceEvaluation struct {
	ID                     string `json:"id"`
	StrategyVersion        string `json:"strategy_version"`
	StrategyManifestSHA256 string `json:"strategy_manifest_sha256"`
	SourceConfigSHA256     string `json:"source_config_sha256"`
	DatasetManifest        string `json:"dataset_manifest"`
	MarketManifest         string `json:"market_manifest"`
	Status                 string `json:"status"`
	Action                 string `json:"action"`
	ObservedAtMS           uint64 `json:"observed_at_ms"`
	EstimatedCostMicros    uint64 `json:"estimated_cost_micros"`
	SourceEpisodeID        string `json:"source_episode_id"`
	PaperEvaluationID      string `json:"paper_evaluation_id"`
	PairIntentID           string `json:"pair_intent_id"`
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

type Dispatch struct {
	EvaluationID       string
	ExecutionAccountID string
	AgentID            string
	ApprovalSHA256     string
	ExpiresAt          time.Time
	Evaluation         SourceEvaluation
	Readiness          Readiness
	AccountState       AccountState
	RequestID          string
	RequestedAtMS      uint64
	QuoteBody          []byte
	QuoteSHA256        string
	RunnerBody         []byte
	RunnerSHA256       string
	OpenEpisode        *OpenEpisode
}

type QuoteRequest struct {
	RequestID          string `json:"request_id"`
	ExecutionAccountID string `json:"execution_account_id"`
	SourceEvaluationID string `json:"source_evaluation_id"`
	MarketManifest     string `json:"market_manifest"`
	IntentID           string `json:"intent_id,omitempty"`
	Action             string `json:"action"`
	RequestedAtMS      uint64 `json:"requested_at_ms"`
}

type QuoteBundle struct {
	SchemaVersion          uint8           `json:"schema_version"`
	ID                     string          `json:"id"`
	RequestID              string          `json:"request_id"`
	ExecutionAccountID     string          `json:"execution_account_id"`
	SourceEvaluationID     string          `json:"source_evaluation_id"`
	MarketManifest         string          `json:"market_manifest"`
	StrategyVersion        string          `json:"strategy_version"`
	StrategyManifestSHA256 string          `json:"strategy_manifest_sha256"`
	SourceConfigSHA256     string          `json:"source_config_sha256"`
	RouteSHA256            string          `json:"route_sha256"`
	OraclePolicySHA256     string          `json:"oracle_policy_sha256"`
	RiskPolicySHA256       string          `json:"risk_policy_sha256"`
	Action                 string          `json:"action"`
	Source                 json.RawMessage `json:"source"`
	Spot                   json.RawMessage `json:"spot"`
	Perp                   json.RawMessage `json:"perp"`
	ExitAuthority          json.RawMessage `json:"exit_authority,omitempty"`
	ObservedAtMS           uint64          `json:"observed_at_ms"`
	ExpiresAtMS            uint64          `json:"expires_at_ms"`
	PublicKey              string          `json:"public_key"`
	Signature              string          `json:"signature"`
}

type perpQuote struct {
	Venue         string `json:"venue"`
	Symbol        string `json:"symbol"`
	MarketIndex   uint32 `json:"market_index"`
	Side          string `json:"side"`
	ReduceOnly    bool   `json:"reduce_only"`
	Phase         string `json:"phase,omitempty"`
	BaseAmount    uint64 `json:"base_amount"`
	BaseDecimals  uint8  `json:"base_decimals"`
	PriceDecimals uint8  `json:"price_decimals"`
	LimitPrice    uint32 `json:"limit_price"`
	MarkPrice     uint32 `json:"mark_price"`
	ObservedAtMS  uint64 `json:"observed_at_ms"`
}

type spotQuote struct {
	Venue                string `json:"venue"`
	Side                 string `json:"side"`
	StockAmount          string `json:"stock_amount"`
	MinimumAmountOut     string `json:"minimum_amount_out"`
	ExpectedUIMultiplier string `json:"expected_ui_multiplier"`
	MinOracleRoundID     string `json:"min_oracle_round_id"`
}

type exitQuoteAuthority struct {
	ExecutionAccountID string `json:"execution_account_id"`
	IntentID           string `json:"intent_id"`
}

type OpenEpisode struct {
	PairIntentID               string `json:"pair_intent_id"`
	SpotUnwindIntentID         string `json:"spot_unwind_intent_id"`
	SpotAmount                 string `json:"spot_amount"`
	MinimumSettlementAmountOut string `json:"minimum_settlement_amount_out"`
	PerpBaseAmount             uint64 `json:"perp_base_amount"`
}

type RunRequest struct {
	Evaluation   SourceEvaluation `json:"evaluation"`
	Readiness    Readiness        `json:"readiness"`
	AccountState AccountState     `json:"account_state"`
	Quotes       json.RawMessage  `json:"quotes"`
	OpenEpisode  *OpenEpisode     `json:"open_episode,omitempty"`
}

type runOutput struct {
	Kind            string          `json:"kind"`
	PairIntent      json.RawMessage `json:"pair_intent,omitempty"`
	Unwind          json.RawMessage `json:"unwind,omitempty"`
	Persistence     json.RawMessage `json:"persistence,omitempty"`
	ExitPersistence json.RawMessage `json:"exit_persistence,omitempty"`
}

type pairIdentity struct {
	ID                     string `json:"id"`
	ExecutionAccountID     string `json:"execution_account_id"`
	AgentID                string `json:"agent_id"`
	SourceEvaluationID     string `json:"source_evaluation_id"`
	StrategyManifestSHA256 string `json:"strategy_manifest_sha256"`
}

type intentPersistence struct {
	Status             string `json:"status"`
	IntentID           string `json:"intent_id"`
	CoordinatorState   string `json:"coordinator_state"`
	CoordinatorVersion uint64 `json:"coordinator_version"`
}

type exitPersistence struct {
	Status             string `json:"status"`
	RequestID          string `json:"request_id"`
	IntentID           string `json:"intent_id"`
	CoordinatorState   string `json:"coordinator_state"`
	CoordinatorVersion uint64 `json:"coordinator_version"`
}

type unwindIdentity struct {
	ID                     string `json:"id"`
	PairIntentID           string `json:"pair_intent_id"`
	ExecutionAccountID     string `json:"execution_account_id"`
	AgentID                string `json:"agent_id"`
	SourceEvaluationID     string `json:"source_evaluation_id"`
	StrategyManifestSHA256 string `json:"strategy_manifest_sha256"`
}

func decodeStrict(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}

func (d Dispatch) validate(now time.Time) error {
	if !accountPattern.MatchString(d.ExecutionAccountID) || !accountPattern.MatchString(d.AgentID) {
		return errors.New("invalid dispatch identity")
	}
	if d.EvaluationID != d.Evaluation.ID || d.Evaluation.ID == "" || !hashPattern.MatchString(d.Evaluation.ID) {
		return errors.New("evaluation identity mismatch")
	}
	if d.Evaluation.StrategyVersion != StrategyVersion || d.Evaluation.StrategyManifestSHA256 != StrategyManifestSHA256 ||
		d.Evaluation.SourceConfigSHA256 != SourceConfigSHA256 || d.Evaluation.Status != "approved" ||
		(d.Evaluation.Action != ActionEntry && d.Evaluation.Action != ActionUnwind) {
		return errors.New("evaluation policy mismatch")
	}
	if !uuidPattern.MatchString(d.Evaluation.SourceEpisodeID) || !uuidPattern.MatchString(d.Evaluation.PaperEvaluationID) ||
		(d.Evaluation.Action == ActionEntry && d.Evaluation.PairIntentID != "") ||
		(d.Evaluation.Action == ActionUnwind && (!hashPattern.MatchString(d.Evaluation.PairIntentID) || d.OpenEpisode == nil ||
			d.OpenEpisode.PairIntentID != d.Evaluation.PairIntentID)) {
		return errors.New("evaluation episode binding mismatch")
	}
	if !hashPattern.MatchString(d.Evaluation.DatasetManifest) || !hashPattern.MatchString(d.Evaluation.MarketManifest) {
		return errors.New("evaluation evidence is invalid")
	}
	if d.ExpiresAt.IsZero() || !now.Before(d.ExpiresAt) || stale(d.Evaluation.ObservedAtMS, now) {
		return errors.New("evaluation is stale")
	}
	if d.Readiness.ExecutionAccountID != d.ExecutionAccountID || d.Readiness.AgentID != d.AgentID ||
		d.Readiness.StrategyVersion != StrategyVersion || d.Readiness.StrategyManifestSHA256 != StrategyManifestSHA256 {
		return errors.New("readiness identity mismatch")
	}
	if d.Readiness.Lifecycle != "running" || !controlAllows(d.Readiness.GlobalControl, d.Evaluation.Action) ||
		!controlAllows(d.Readiness.StrategyControl, d.Evaluation.Action) || !controlAllows(d.Readiness.AccountControl, d.Evaluation.Action) ||
		!d.Readiness.FullyVerified || !d.Readiness.VaultWired || !d.Readiness.VaultFunded ||
		!d.Readiness.ExecutionSignerFunded || !d.Readiness.LighterLinked || !d.Readiness.LighterFunded ||
		!d.Readiness.RouteHealthy || !d.Readiness.OracleHealthy || !d.Readiness.SequencerHealthy ||
		stale(d.Readiness.ObservedAtMS, now) {
		return errors.New("readiness is not live")
	}
	state := d.AccountState
	if state.ExecutionAccountID != d.ExecutionAccountID || state.AgentID != d.AgentID ||
		state.StrategyManifestSHA256 != StrategyManifestSHA256 || state.LighterAccountIndex == 0 ||
		state.LighterAPIKeyIndex < 4 || stale(state.ObservedAtMS, now) {
		return errors.New("account state identity mismatch")
	}
	if !state.LighterNonceAligned || !state.RobinhoodNonceAligned || state.UnknownLighterOrders ||
		state.UnknownLighterPositions || state.UnknownRobinhoodPosition {
		return errors.New("account state is not reconciled")
	}
	if d.Evaluation.Action == ActionEntry && (state.ActiveEpisodes != 0 || !state.Flat) {
		return errors.New("account state is not entry-safe")
	}
	if d.Evaluation.Action == ActionUnwind && (state.ActiveEpisodes != 1 || state.Flat || !validOpenEpisode(*d.OpenEpisode)) {
		return errors.New("account state is not unwind-safe")
	}
	if d.Evaluation.Action == ActionEntry && (state.CollateralMicros == 0 ||
		(state.MaintenanceMarginMicros > 0 && state.CollateralMicros/2 < state.MaintenanceMarginMicros)) {
		return errors.New("margin coverage is insufficient")
	}
	material, err := approvalMaterial(d)
	if err != nil {
		return err
	}
	if d.ApprovalSHA256 != digest(material) {
		return errors.New("approval digest mismatch")
	}
	return nil
}

func controlAllows(mode, action string) bool {
	return mode == "ACTIVE" || action == ActionUnwind && mode == "REDUCE_ONLY"
}

func validOpenEpisode(episode OpenEpisode) bool {
	return hashPattern.MatchString(episode.PairIntentID) && hashPattern.MatchString(episode.SpotUnwindIntentID) &&
		positiveDecimal(episode.SpotAmount) && positiveDecimal(episode.MinimumSettlementAmountOut) && episode.PerpBaseAmount > 0
}

func positiveDecimal(value string) bool {
	if value == "" || value == "0" {
		return false
	}
	for index, char := range value {
		if char < '0' || char > '9' || index == 0 && char == '0' {
			return false
		}
	}
	return true
}

func stale(observed uint64, now time.Time) bool {
	nowMS := uint64(now.UnixMilli())
	return observed > nowMS || nowMS-observed > uint64(maxEvidenceAge/time.Millisecond)
}

func approvalMaterial(d Dispatch) ([]byte, error) {
	return json.Marshal(struct {
		EvaluationAccountID string           `json:"execution_account_id"`
		AgentID             string           `json:"agent_id"`
		Evaluation          SourceEvaluation `json:"evaluation"`
		Readiness           Readiness        `json:"readiness"`
		AccountState        AccountState     `json:"account_state"`
		ExpiresAtMS         int64            `json:"expires_at_ms"`
	}{d.ExecutionAccountID, d.AgentID, d.Evaluation, d.Readiness, d.AccountState, d.ExpiresAt.UnixMilli()})
}

func ApprovalSHA256(
	executionAccountID, agentID string,
	evaluation SourceEvaluation,
	readiness Readiness,
	accountState AccountState,
	expiresAt time.Time,
) (string, error) {
	material, err := approvalMaterial(Dispatch{
		ExecutionAccountID: executionAccountID,
		AgentID:            agentID,
		Evaluation:         evaluation,
		Readiness:          readiness,
		AccountState:       accountState,
		ExpiresAt:          expiresAt,
	})
	if err != nil {
		return "", err
	}
	return digest(material), nil
}

func requestID(evaluationID, executionAccountID, action, intentID string) string {
	h := sha256.New()
	h.Write([]byte("robin.live.scheduler.quote-request.v2\x00"))
	h.Write([]byte(evaluationID))
	h.Write([]byte{0})
	h.Write([]byte(executionAccountID))
	h.Write([]byte{0})
	h.Write([]byte(action))
	h.Write([]byte{0})
	h.Write([]byte(intentID))
	return "0x" + hex.EncodeToString(h.Sum(nil))
}

func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func validateRunnerOutput(body []byte, dispatch Dispatch) error {
	var output runOutput
	if err := decodeStrict(body, &output); err != nil {
		return fmt.Errorf("decode runner output: %w", err)
	}
	if dispatch.Evaluation.Action == ActionEntry {
		if output.Kind != ActionEntry || len(output.PairIntent) == 0 || len(output.Unwind) != 0 || len(output.ExitPersistence) != 0 {
			return errors.New("runner output kind mismatch")
		}
		var intent pairIdentity
		if err := json.Unmarshal(output.PairIntent, &intent); err != nil {
			return errors.New("invalid runner intent")
		}
		if intent.ID == "" || intent.ExecutionAccountID != dispatch.ExecutionAccountID || intent.AgentID != dispatch.AgentID ||
			intent.SourceEvaluationID != dispatch.EvaluationID || intent.StrategyManifestSHA256 != StrategyManifestSHA256 {
			return errors.New("runner intent identity mismatch")
		}
		var persistence intentPersistence
		if err := decodeStrict(output.Persistence, &persistence); err != nil || persistence.Status != "persisted" ||
			persistence.IntentID != intent.ID || persistence.CoordinatorVersion == 0 {
			return errors.New("runner persistence mismatch")
		}
		return nil
	}
	if output.Kind != ActionUnwind || len(output.Unwind) == 0 || len(output.PairIntent) != 0 || len(output.Persistence) != 0 {
		return errors.New("runner unwind output kind mismatch")
	}
	var unwind unwindIdentity
	if err := json.Unmarshal(output.Unwind, &unwind); err != nil || !hashPattern.MatchString(unwind.ID) ||
		unwind.PairIntentID != dispatch.Evaluation.PairIntentID || unwind.ExecutionAccountID != dispatch.ExecutionAccountID ||
		unwind.AgentID != dispatch.AgentID || unwind.SourceEvaluationID != dispatch.EvaluationID ||
		unwind.StrategyManifestSHA256 != StrategyManifestSHA256 {
		return errors.New("runner unwind identity mismatch")
	}
	var persistence exitPersistence
	if err := decodeStrict(output.ExitPersistence, &persistence); err != nil || persistence.Status != "persisted" ||
		persistence.RequestID != unwind.ID || persistence.IntentID != unwind.PairIntentID || persistence.CoordinatorVersion == 0 {
		return errors.New("runner exit persistence mismatch")
	}
	return nil
}

func validateQuote(body []byte, request QuoteRequest, publicKey ed25519.PublicKey, lighterMarket uint32, now time.Time) (QuoteBundle, error) {
	var quote QuoteBundle
	if err := decodeStrict(body, &quote); err != nil {
		return quote, fmt.Errorf("decode quote: %w", err)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return quote, errors.New("invalid trusted quote key")
	}
	embedded, err := base64.StdEncoding.DecodeString(quote.PublicKey)
	if err != nil || len(embedded) != ed25519.PublicKeySize || subtle.ConstantTimeCompare(embedded, publicKey) != 1 {
		return quote, errors.New("quote key mismatch")
	}
	signature, err := base64.StdEncoding.DecodeString(quote.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return quote, errors.New("invalid quote signature")
	}
	wantID, err := quote.calculateID()
	if err != nil || wantID != quote.ID {
		return quote, errors.New("quote ID mismatch")
	}
	material, err := quote.signatureMaterial()
	if err != nil || !ed25519.Verify(publicKey, material, signature) {
		return quote, errors.New("quote signature mismatch")
	}
	if quote.SchemaVersion != quoteSchemaVersion || quote.RequestID != request.RequestID ||
		quote.ExecutionAccountID != request.ExecutionAccountID || quote.SourceEvaluationID != request.SourceEvaluationID ||
		quote.MarketManifest != request.MarketManifest || quote.Action != request.Action ||
		quote.StrategyVersion != StrategyVersion || quote.StrategyManifestSHA256 != StrategyManifestSHA256 ||
		quote.SourceConfigSHA256 != SourceConfigSHA256 || quote.RouteSHA256 != routeSHA256 ||
		quote.OraclePolicySHA256 != oraclePolicySHA256 || quote.RiskPolicySHA256 != riskPolicySHA256 {
		return quote, errors.New("quote identity or policy mismatch")
	}
	nowMS := uint64(now.UnixMilli())
	if quote.ObservedAtMS > nowMS || quote.ExpiresAtMS <= nowMS || quote.ExpiresAtMS <= quote.ObservedAtMS ||
		quote.ExpiresAtMS-quote.ObservedAtMS > uint64(maxEvidenceAge/time.Millisecond) || len(quote.Source) == 0 || len(quote.Spot) == 0 || len(quote.Perp) == 0 {
		return quote, errors.New("quote is stale or incomplete")
	}
	var spot spotQuote
	var perp perpQuote
	if err := json.Unmarshal(quote.Spot, &spot); err != nil || json.Unmarshal(quote.Perp, &perp) != nil ||
		perp.Venue != "lighter-mainnet" || perp.Symbol != "AAPL" || perp.MarketIndex != lighterMarket ||
		perp.ObservedAtMS != quote.ObservedAtMS || spot.Venue != "robinhood-chain-mainnet" ||
		!positiveDecimal(spot.StockAmount) || !positiveDecimal(spot.MinimumAmountOut) ||
		!positiveDecimal(spot.ExpectedUIMultiplier) || !positiveDecimal(spot.MinOracleRoundID) {
		return quote, errors.New("quote Lighter market identity mismatch")
	}
	if request.Action == ActionEntry {
		if request.IntentID != "" || len(quote.ExitAuthority) != 0 || spot.Side != "buy" || perp.Side != "short" || perp.ReduceOnly || perp.Phase != "" {
			return quote, errors.New("entry quote shape mismatch")
		}
		return quote, nil
	}
	var authority exitQuoteAuthority
	if err := json.Unmarshal(quote.ExitAuthority, &authority); err != nil {
		return quote, errors.New("unwind quote binding mismatch")
	}
	if !hashPattern.MatchString(request.IntentID) ||
		authority.ExecutionAccountID != request.ExecutionAccountID || authority.IntentID != request.IntentID ||
		spot.Side != "sell" || perp.Side != "long" || !perp.ReduceOnly ||
		(perp.Phase != "perp_and_spot" && perp.Phase != "spot_only") {
		return quote, errors.New("unwind quote binding mismatch")
	}
	return quote, nil
}

func (q QuoteBundle) calculateID() (string, error) {
	material := q
	material.ID = ""
	material.Signature = ""
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte("robin.live.quote-bundle.v1\x00"))
	h.Write(encoded)
	return "0x" + hex.EncodeToString(h.Sum(nil)), nil
}

func (q QuoteBundle) signatureMaterial() ([]byte, error) {
	material := q
	material.Signature = ""
	encoded, err := json.Marshal(material)
	if err != nil {
		return nil, err
	}
	return append([]byte("robin.live.quote-signature.v1\x00"), encoded...), nil
}
