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
	Lighter            LighterBinding   `json:"lighter"`
	Robinhood          RobinhoodBinding `json:"robinhood"`
}

type LighterBinding struct {
	AccountIndex         uint64 `json:"accountIndex"`
	APIKeyIndex          uint8  `json:"apiKeyIndex"`
	MarketID             uint16 `json:"marketId"`
	ReadOnlyTokenFile    string `json:"readOnlyTokenFile"`
	ExpectedNonceFile    string `json:"expectedNonceFile"`
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
	ReceiptJournalFile   string   `json:"receiptJournalFile"`
	ReceiptHashes        []string `json:"-"`
}

type LighterObservation struct {
	AccountIndex                 uint64
	APIKeyIndex                  uint8
	Nonce                        uint64
	ExpectedNonce                uint64
	CollateralRaw                string
	MaintenanceRequirementRaw    string
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
	return o.AccountIndex > 0 && o.APIKeyIndex >= 2 && o.APIKeyIndex <= 254 &&
		o.Nonce == o.ExpectedNonce && o.NoUnknownOrders && o.NoUnknownPositions &&
		o.CollateralReady && o.RESTReconstructed &&
		o.MaintenanceMarginRatioMicros >= 2_000_000 && fresh(o.ObservedAt, time.Now())
}

type RobinhoodObservation struct {
	Vault                string
	Signer               string
	Owner                string
	SettlementBalanceRaw string
	OwnerGasRaw          string
	SignerGasRaw         string
	AgentEnabled         bool
	Flat                 bool
	WiringVerified       bool
	FinalityHealthy      bool
	FundingReady         bool
	OwnerGasReady        bool
	SignerGasReady       bool
	RiskMode             string
	FinalizedNumber      uint64
	FinalizedHash        string
	ObservedAt           time.Time
}

func (o RobinhoodObservation) Healthy() bool {
	return o.WiringVerified && o.FinalityHealthy && o.FundingReady &&
		o.OwnerGasReady && o.SignerGasReady && fresh(o.ObservedAt, time.Now())
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
	NonceAligned                 bool   `json:"nonce_aligned"`
	NoUnknownOrders              bool   `json:"no_unknown_orders"`
	NoUnknownPositions           bool   `json:"no_unknown_positions"`
	CollateralReady              bool   `json:"collateral_ready"`
	MaintenanceMarginRatioMicros uint64 `json:"maintenance_margin_ratio_micros"`
	Flat                         bool   `json:"flat"`
}

type RobinhoodPayload struct {
	VaultAddress         string `json:"vault_address"`
	SignerAddress        string `json:"signer_address"`
	FundingReady         bool   `json:"funding_ready"`
	WiringVerified       bool   `json:"wiring_verified"`
	FinalityHealthy      bool   `json:"finality_healthy"`
	Flat                 bool   `json:"flat"`
	OwnerAddress         string `json:"owner_address"`
	AgentEnabled         bool   `json:"agent_enabled"`
	RiskMode             string `json:"risk_mode"`
	SettlementBalanceRaw string `json:"settlement_balance_raw"`
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
