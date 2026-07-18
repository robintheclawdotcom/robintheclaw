package publisher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	mainnetChainID = uint64(4663)
	maxEvidenceAge = 5 * time.Second
)

var ErrRateLimited = errors.New("upstream rate limited")

type AccountBinding struct {
	ExecutionAccountID string           `json:"executionAccountId"`
	ReadinessAccountID string           `json:"readinessExecutionAccountId,omitempty"`
	PolicyActive       bool             `json:"policyActive"`
	StrategyVersion    string           `json:"strategyVersion"`
	Lighter            LighterBinding   `json:"lighter"`
	Robinhood          RobinhoodBinding `json:"robinhood"`
}

type LighterBinding struct {
	AccountIndex         uint64 `json:"accountIndex"`
	APIKeyIndex          uint8  `json:"apiKeyIndex"`
	MarketID             uint16 `json:"marketId"`
	MinimumCollateralRaw string `json:"minimumCollateralRaw"`
}

type RobinhoodBinding struct {
	Registry             string   `json:"registry"`
	Factory              string   `json:"factory"`
	Vault                string   `json:"vault"`
	RiskManager          string   `json:"riskManager"`
	SpotAdapter          string   `json:"spotAdapter"`
	Owner                string   `json:"owner"`
	Signer               string   `json:"signer"`
	VaultCodeHash        string   `json:"vaultCodeHash"`
	MinimumSettlementRaw string   `json:"minimumSettlementRaw"`
	MinimumOwnerGasRaw   string   `json:"minimumOwnerGasRaw"`
	MinimumSignerGasRaw  string   `json:"minimumSignerGasRaw"`
	ReceiptHashes        []string `json:"receiptHashes"`
	ExpectedSignerNonce  uint64   `json:"expectedSignerNonce"`
	SignerJournalReady   bool     `json:"signerJournalReady"`
}

type LighterObservation struct {
	AccountIndex                 uint64
	APIKeyIndex                  uint8
	MarketID                     uint16
	Nonce                        uint64
	ExpectedNonce                uint64
	CollateralRaw                string
	MaintenanceRequirementRaw    string
	CollateralMicros             uint64
	MaintenanceMarginMicros      uint64
	MaintenanceMarginRatioMicros uint64
	NoUnknownOrders              bool
	NoUnknownPositions           bool
	CollateralReady              bool
	Flat                         bool
	RESTReconstructed            bool
	TradeCount                   int
	LastTradeID                  uint64
	StateDigest                  string
	ObservedAt                   time.Time
}

func (o LighterObservation) Healthy() bool {
	return o.AccountIndex > 0 && o.APIKeyIndex >= 4 && o.APIKeyIndex <= 254 &&
		o.Nonce == o.ExpectedNonce && o.NoUnknownOrders && o.NoUnknownPositions &&
		o.CollateralReady && o.RESTReconstructed &&
		o.MaintenanceMarginRatioMicros >= 2_000_000 && fresh(o.ObservedAt, time.Now())
}

type RobinhoodObservation struct {
	Vault                 string
	Signer                string
	Owner                 string
	SettlementBalanceRaw  string
	OwnerGasRaw           string
	SignerGasRaw          string
	AgentEnabled          bool
	FinalizedAgentAddress string
	FinalizedAgentEnabled bool
	FinalizedAgentRevoked bool
	Flat                  bool
	WiringVerified        bool
	FinalityHealthy       bool
	FundingReady          bool
	OwnerGasReady         bool
	SignerGasReady        bool
	OracleHealthy         bool
	SequencerHealthy      bool
	GlobalMode            string
	FinalizedGlobalMode   string
	RiskMode              string
	FinalizedRiskMode     string
	SignerNonceAligned    bool
	SpotConfigVersion     uint64
	StockDecimals         uint8
	UIMultiplierE18       string
	NewUIMultiplierE18    string
	OraclePaused          bool
	FinalizedNumber       uint64
	FinalizedHash         string
	FinalizedTimestamp    uint64
	SourceBlockNumber     uint64
	SourceBlockHash       string
	SourceBlockTimestamp  uint64
	ObservedAt            time.Time
}

func (o RobinhoodObservation) Healthy() bool {
	return o.WiringVerified && o.FinalityHealthy && o.FundingReady &&
		o.entryAuthorized() &&
		o.OwnerGasReady && o.SignerGasReady && o.SignerNonceAligned && o.SpotConfigVersion > 0 &&
		o.StockDecimals <= 18 && o.UIMultiplierE18 != "" &&
		o.UIMultiplierE18 == o.NewUIMultiplierE18 && !o.OraclePaused && o.OracleHealthy &&
		o.SequencerHealthy && o.sourceBound() && fresh(o.ObservedAt, time.Now())
}

func (o RobinhoodObservation) entryAuthorized() bool {
	return o.AgentEnabled && o.FinalizedAgentEnabled && !o.FinalizedAgentRevoked &&
		strings.EqualFold(o.FinalizedAgentAddress, o.Signer) &&
		o.GlobalMode == "ACTIVE" && o.FinalizedGlobalMode == "ACTIVE" &&
		o.RiskMode == "ACTIVE" && o.FinalizedRiskMode == "ACTIVE"
}

func (o RobinhoodObservation) sourceBound() bool {
	if o.FinalizedNumber == 0 || !validHash(o.FinalizedHash) || o.FinalizedTimestamp == 0 ||
		o.SourceBlockNumber < o.FinalizedNumber || !validHash(o.SourceBlockHash) ||
		o.SourceBlockTimestamp < o.FinalizedTimestamp ||
		o.SourceBlockTimestamp-o.FinalizedTimestamp > uint64(maxFinalizedEvidenceAge/time.Second) ||
		o.ObservedAt.Unix() < 0 || uint64(o.ObservedAt.Unix()) != o.SourceBlockTimestamp ||
		!o.ObservedAt.Equal(time.Unix(o.ObservedAt.Unix(), 0)) {
		return false
	}
	return o.SourceBlockNumber != o.FinalizedNumber || o.SourceBlockHash == o.FinalizedHash
}

type CoordinatorSnapshot struct {
	ExecutionAccountID string      `json:"execution_account_id"`
	Source             string      `json:"source"`
	SourceSession      string      `json:"source_session"`
	SourceSequence     int64       `json:"source_sequence"`
	Payload            interface{} `json:"payload"`
	ObservedAtMS       int64       `json:"observed_at_ms"`
	ReceivedAtMS       int64       `json:"received_at_ms"`
	ExpiresAtMS        int64       `json:"expires_at_ms"`
}

type LighterPayload struct {
	AccountIndex                 uint64 `json:"account_index"`
	APIKeyIndex                  uint8  `json:"api_key_index"`
	MarketIndex                  uint16 `json:"market_index"`
	NonceAligned                 bool   `json:"nonce_aligned"`
	NoUnknownOrders              bool   `json:"no_unknown_orders"`
	NoUnknownPositions           bool   `json:"no_unknown_positions"`
	CollateralReady              bool   `json:"collateral_ready"`
	MaintenanceMarginRatioMicros uint64 `json:"maintenance_margin_ratio_micros"`
	CollateralMicros             uint64 `json:"collateral_micros"`
	MaintenanceMarginMicros      uint64 `json:"maintenance_margin_micros"`
	Flat                         bool   `json:"flat"`
}

type RobinhoodPayload struct {
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
	FinalizedAgentRevoked bool   `json:"finalized_agent_revoked"`
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

type ReadinessSnapshot struct {
	ExecutionAccountID string              `json:"executionAccountId"`
	Evidence           []ReadinessEvidence `json:"evidence"`
}

type ReadinessEvidence struct {
	CheckName      string    `json:"checkName"`
	Ready          bool      `json:"ready"`
	Source         string    `json:"source"`
	EvidenceDigest string    `json:"evidenceDigest"`
	ObservedAt     time.Time `json:"observedAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

func EvidenceDigest(value interface{}) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func fresh(observed, now time.Time) bool {
	age := now.Sub(observed)
	return !observed.IsZero() && age >= 0 && age <= maxEvidenceAge
}

func validAddress(value string) bool {
	if len(value) != 42 || !strings.HasPrefix(value, "0x") {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil && value != "0x"+strings.Repeat("0", 40)
}

func normalizeAddress(value string) (string, error) {
	value = strings.ToLower(value)
	if !validAddress(value) {
		return "", errors.New("invalid EVM address")
	}
	return value, nil
}

func parseUnsignedDecimal(value string) (*big.Rat, error) {
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "eE+") {
		return nil, errors.New("invalid decimal")
	}
	ratio, ok := new(big.Rat).SetString(value)
	if !ok || ratio.Sign() < 0 {
		return nil, errors.New("invalid decimal")
	}
	return ratio, nil
}

func decimalAtLeast(value, minimum string) bool {
	left, err := parseUnsignedDecimal(value)
	if err != nil {
		return false
	}
	right, err := parseUnsignedDecimal(minimum)
	return err == nil && left.Cmp(right) >= 0
}

func marginRatioMicros(collateral, maintenance string) (uint64, error) {
	c, err := parseUnsignedDecimal(collateral)
	if err != nil {
		return 0, err
	}
	m, err := parseUnsignedDecimal(maintenance)
	if err != nil {
		return 0, err
	}
	if m.Sign() == 0 {
		return 10_000_000, nil
	}
	ratio := new(big.Rat).Mul(new(big.Rat).Quo(c, m), big.NewRat(1_000_000, 1))
	if !ratio.IsInt() {
		ratio = new(big.Rat).SetInt(new(big.Int).Quo(ratio.Num(), ratio.Denom()))
	}
	if !ratio.Num().IsUint64() {
		return 0, fmt.Errorf("maintenance margin ratio out of range")
	}
	return ratio.Num().Uint64(), nil
}

func decimalMicros(value string) (uint64, error) {
	ratio, err := parseUnsignedDecimal(value)
	if err != nil {
		return 0, err
	}
	scaled := new(big.Rat).Mul(ratio, big.NewRat(1_000_000, 1))
	if !scaled.IsInt() || !scaled.Num().IsUint64() {
		return 0, errors.New("decimal cannot be represented as micros")
	}
	return scaled.Num().Uint64(), nil
}
