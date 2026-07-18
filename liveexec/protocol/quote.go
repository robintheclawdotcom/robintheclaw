package protocol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const (
	StrategyVersion                = "basis-aapl-v1"
	StrategyManifestSHA256         = "7787f323c898f08bec51028ced5ee402f18f85da891515306ee330b2171c3902"
	PreviousStrategyManifestSHA256 = "da181add4750de3e3bc58606f6e0c1c2686a0206cc3f56ac3f0ba0c8f5c2868f"
	LegacyStrategyManifestSHA256   = "4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a"
	SourceConfigSHA256             = "59106a18758a95af45e6ac1a8257843cfbd2a45fd09b5b3c3f429d3dedb56c2a"
	RouteSHA256                    = "77d59f5e80e76ed507522b27ee6b7ddd1f8395f0337f0b230c5bba64bb335590"
	OraclePolicySHA256             = "7f0d306267da767869c0bc5951ce911ac1cb9060294edfa8eeefa884e0ddf937"
	RiskPolicySHA256               = "a5f01d41a420d3b077ad75814dd26356a1431acea18593d3f2d359c1f686104e"
	RiskVersion                    = StrategyVersion
	ChainID                        = uint64(4663)
	Symbol                         = "AAPL"
	SpotVenue                      = "robinhood-chain-mainnet"
	PerpVenue                      = "lighter-mainnet"
	SettlementToken                = "0x5fc5360d0400a0fd4f2af552add042d716f1d168"
	StockToken                     = "0xaf3d76f1834a1d425780943c99ea8a608f8a93f9"
	Router                         = "0x8876789976decbfcbbbe364623c63652db8c0904"
	EntryNotionalMicros            = uint64(25_000_000)
	MaximumQuoteLifetimeMS         = uint64(5_000)
	MaximumExitReconciliationMS    = uint64(24 * 60 * 60 * 1_000)
	QuoteSchemaVersion             = uint8(4)
)

var (
	quoteIDDomain        = []byte("robin.live.quote-bundle.v1\x00")
	quoteSignatureDomain = []byte("robin.live.quote-signature.v1\x00")
)

type Action string

const (
	ActionEntry  Action = "entry"
	ActionUnwind Action = "unwind"
)

type QuoteRequest struct {
	RequestID                    string `json:"request_id"`
	ExecutionAccountID           string `json:"execution_account_id"`
	SourceEvaluationID           string `json:"source_evaluation_id"`
	MarketManifest               string `json:"market_manifest"`
	IntentID                     string `json:"intent_id,omitempty"`
	TargetStrategyManifestSHA256 string `json:"target_strategy_manifest_sha256,omitempty"`
	Action                       Action `json:"action"`
	RequestedAtMS                uint64 `json:"requested_at_ms"`
}

type SourceIdentity struct {
	AdapterID   string `json:"adapter_id"`
	SpotSource  string `json:"spot_source"`
	PerpSource  string `json:"perp_source"`
	OracleRound string `json:"oracle_round"`
}

type SpotQuote struct {
	Venue                string `json:"venue"`
	ChainID              uint64 `json:"chain_id"`
	SettlementToken      string `json:"settlement_token"`
	StockToken           string `json:"stock_token"`
	Router               string `json:"router"`
	Side                 string `json:"side"`
	SettlementAmount     string `json:"settlement_amount"`
	StockAmount          string `json:"stock_amount"`
	MinimumAmountOut     string `json:"minimum_amount_out"`
	ExpectedUIMultiplier string `json:"expected_ui_multiplier"`
	MinOracleRoundID     string `json:"min_oracle_round_id"`
	ReferencePriceMicros uint64 `json:"reference_price_micros"`
	BlockHash            string `json:"block_hash"`
	ObservedAtMS         uint64 `json:"observed_at_ms"`
}

type PerpQuote struct {
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

type ExitQuoteAuthority struct {
	Source                   string `json:"source"`
	SourceSession            string `json:"source_session"`
	SourceEventID            string `json:"source_event_id"`
	SourceSequence           int64  `json:"source_sequence"`
	ExecutionAccountID       string `json:"execution_account_id"`
	IntentID                 string `json:"intent_id"`
	MarketManifest           string `json:"market_manifest"`
	PayloadSHA256            string `json:"payload_sha256"`
	ReceivedAtMS             uint64 `json:"received_at_ms"`
	SubmissionDeadlineMS     uint64 `json:"submission_deadline_ms"`
	ReconciliationDeadlineMS uint64 `json:"reconciliation_deadline_ms"`
}

type MarketQuotePublication struct {
	Source                       string  `json:"source"`
	SourceSession                string  `json:"source_session"`
	SourceEventID                string  `json:"source_event_id"`
	SourceSequence               int64   `json:"source_sequence"`
	ExecutionAccountID           string  `json:"execution_account_id,omitempty"`
	MarketManifest               string  `json:"market_manifest"`
	StrategyManifestSHA256       string  `json:"strategy_manifest_sha256,omitempty"`
	TargetStrategyManifestSHA256 string  `json:"target_strategy_manifest_sha256,omitempty"`
	RouteSHA256                  string  `json:"route_sha256,omitempty"`
	LighterMarketIndex           uint32  `json:"lighter_market_index,omitempty"`
	QuoteBlockHash               string  `json:"quote_block_hash"`
	MarkPrice                    uint32  `json:"mark_price"`
	ExpectedUIMultiplier         string  `json:"expected_ui_multiplier"`
	MinOracleRoundID             string  `json:"min_oracle_round_id"`
	PublisherAtMS                int64   `json:"publisher_at_ms"`
	ReceivedAtMS                 int64   `json:"received_at_ms"`
	ExpiresAtMS                  int64   `json:"expires_at_ms"`
	IntentID                     string  `json:"intent_id,omitempty"`
	SpotUnwindAmountIn           string  `json:"spot_unwind_amount_in,omitempty"`
	SpotUnwindExpectedAmountOut  string  `json:"spot_unwind_expected_amount_out,omitempty"`
	UnwindPhase                  string  `json:"unwind_phase,omitempty"`
	PerpUnwindBaseAmount         *uint64 `json:"perp_unwind_base_amount,omitempty"`
	PerpUnwindLimitPrice         uint32  `json:"perp_unwind_limit_price,omitempty"`
	SubmissionDeadlineMS         int64   `json:"submission_deadline_ms,omitempty"`
	ReconciliationDeadlineMS     int64   `json:"reconciliation_deadline_ms,omitempty"`
}

type MarketQuoteReceipt struct {
	Status        string `json:"status"`
	SourceSession string `json:"source_session"`
	SourceEventID string `json:"source_event_id"`
	PayloadSHA256 string `json:"payload_sha256"`
}

type QuoteBundle struct {
	SchemaVersion                uint8               `json:"schema_version"`
	ID                           string              `json:"id"`
	RequestID                    string              `json:"request_id"`
	ExecutionAccountID           string              `json:"execution_account_id"`
	SourceEvaluationID           string              `json:"source_evaluation_id"`
	MarketManifest               string              `json:"market_manifest"`
	StrategyVersion              string              `json:"strategy_version"`
	StrategyManifestSHA256       string              `json:"strategy_manifest_sha256"`
	TargetStrategyManifestSHA256 string              `json:"target_strategy_manifest_sha256,omitempty"`
	SourceConfigSHA256           string              `json:"source_config_sha256"`
	RouteSHA256                  string              `json:"route_sha256"`
	OraclePolicySHA256           string              `json:"oracle_policy_sha256"`
	RiskPolicySHA256             string              `json:"risk_policy_sha256"`
	Action                       Action              `json:"action"`
	Source                       SourceIdentity      `json:"source"`
	Spot                         SpotQuote           `json:"spot"`
	Perp                         PerpQuote           `json:"perp"`
	ExitAuthority                *ExitQuoteAuthority `json:"exit_authority,omitempty"`
	ObservedAtMS                 uint64              `json:"observed_at_ms"`
	ExpiresAtMS                  uint64              `json:"expires_at_ms"`
	PublicKey                    string              `json:"public_key"`
	Signature                    string              `json:"signature"`
}

func (q *QuoteBundle) Sign(privateKey ed25519.PrivateKey) error {
	if len(privateKey) != ed25519.PrivateKeySize {
		return errors.New("invalid quote signing key")
	}
	q.PublicKey = base64.StdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey))
	id, err := q.calculateID()
	if err != nil {
		return err
	}
	q.ID = id
	material, err := q.signatureMaterial()
	if err != nil {
		return err
	}
	q.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, material))
	return nil
}

func (q QuoteBundle) Verify(expectedPublicKey ed25519.PublicKey, expectedMarketIndex uint32, nowMS uint64) error {
	if len(expectedPublicKey) != ed25519.PublicKeySize {
		return errors.New("invalid trusted quote key")
	}
	if expectedMarketIndex > 32767 {
		return errors.New("invalid trusted market index")
	}
	embedded, err := base64.StdEncoding.DecodeString(q.PublicKey)
	if err != nil || len(embedded) != ed25519.PublicKeySize || subtle.ConstantTimeCompare(embedded, expectedPublicKey) != 1 {
		return errors.New("quote key mismatch")
	}
	signature, err := base64.StdEncoding.DecodeString(q.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("invalid quote signature")
	}
	calculatedID, err := q.calculateID()
	if err != nil || calculatedID != q.ID {
		return errors.New("quote id mismatch")
	}
	material, err := q.signatureMaterial()
	if err != nil || !ed25519.Verify(expectedPublicKey, material, signature) {
		return errors.New("quote signature mismatch")
	}
	if err := q.validateCanonical(expectedMarketIndex); err != nil {
		return err
	}
	if q.ObservedAtMS > nowMS || q.ExpiresAtMS <= nowMS || nowMS-q.ObservedAtMS > MaximumQuoteLifetimeMS {
		return errors.New("quote is stale")
	}
	return nil
}

func (q QuoteBundle) validateCanonical(expectedMarketIndex uint32) error {
	if q.SchemaVersion != QuoteSchemaVersion || q.StrategyVersion != StrategyVersion || q.StrategyManifestSHA256 != StrategyManifestSHA256 ||
		q.SourceConfigSHA256 != SourceConfigSHA256 || q.RouteSHA256 != RouteSHA256 ||
		q.OraclePolicySHA256 != OraclePolicySHA256 || q.RiskPolicySHA256 != RiskPolicySHA256 {
		return errors.New("quote policy mismatch")
	}
	if !validHash(q.ID) || !validHash(q.RequestID) || !validHash(q.SourceEvaluationID) ||
		!validHash(q.MarketManifest) || !validHash(q.Spot.BlockHash) {
		return errors.New("quote identity is invalid")
	}
	if !validExecutionID(q.ExecutionAccountID) || q.Source.AdapterID == "" || q.Source.SpotSource == "" ||
		q.Source.PerpSource == "" || q.Source.OracleRound == "" {
		return errors.New("quote source is incomplete")
	}
	if q.Action != ActionEntry && q.Action != ActionUnwind {
		return errors.New("quote action is invalid")
	}
	if q.Action == ActionEntry && (q.TargetStrategyManifestSHA256 != "" || q.ExitAuthority != nil) {
		return errors.New("entry quote contains an unwind binding")
	}
	if q.Action == ActionUnwind &&
		(!IsAllowedUnwindTargetStrategyManifest(q.TargetStrategyManifestSHA256) || !q.validExitAuthority()) {
		return errors.New("exit quote authority is invalid")
	}
	if q.Spot.Venue != SpotVenue || q.Spot.ChainID != ChainID || q.Spot.SettlementToken != SettlementToken ||
		q.Spot.StockToken != StockToken || q.Spot.Router != Router || q.Perp.Venue != PerpVenue || q.Perp.Symbol != Symbol {
		return errors.New("quote route mismatch")
	}
	wantSpotSide, wantPerpSide, wantReduceOnly := "buy", "short", false
	if q.Action == ActionUnwind {
		wantSpotSide, wantPerpSide, wantReduceOnly = "sell", "long", true
	}
	if q.Spot.Side != wantSpotSide || q.Perp.Side != wantPerpSide || q.Perp.ReduceOnly != wantReduceOnly {
		return errors.New("quote direction mismatch")
	}
	if q.Action == ActionEntry && q.Perp.Phase != "" {
		return errors.New("entry quote contains an unwind phase")
	}
	if q.Action == ActionUnwind && q.Perp.Phase != "perp_and_spot" && q.Perp.Phase != "spot_only" {
		return errors.New("unwind quote phase is invalid")
	}
	spotOnly := q.Action == ActionUnwind && q.Perp.Phase == "spot_only"
	if q.Spot.SettlementAmount == "" || q.Spot.StockAmount == "" || q.Spot.MinimumAmountOut == "" ||
		q.Spot.ReferencePriceMicros == 0 || (!spotOnly && q.Perp.BaseAmount == 0) || (spotOnly && q.Perp.BaseAmount != 0) ||
		q.Perp.LimitPrice == 0 || q.Perp.MarkPrice == 0 ||
		q.Perp.MarketIndex != expectedMarketIndex || q.Perp.BaseDecimals > 18 || q.Perp.PriceDecimals > 18 {
		return errors.New("quote amounts are invalid")
	}
	settlement, settlementOK := positiveDecimal(q.Spot.SettlementAmount)
	stock, stockOK := positiveDecimal(q.Spot.StockAmount)
	minimum, minimumOK := positiveDecimal(q.Spot.MinimumAmountOut)
	multiplier, multiplierOK := positiveDecimal(q.Spot.ExpectedUIMultiplier)
	minimumRound, minimumRoundOK := positiveDecimal(q.Spot.MinOracleRoundID)
	if !settlementOK || !stockOK || !minimumOK || !multiplierOK || !minimumRoundOK ||
		multiplier.BitLen() > 256 || minimumRound.BitLen() > 80 || q.Source.OracleRound != q.Spot.MinOracleRoundID ||
		(q.Action == ActionEntry && minimum.Cmp(stock) > 0) ||
		(q.Action == ActionUnwind && minimum.Cmp(settlement) > 0) {
		return errors.New("quote amounts are invalid")
	}
	if q.ObservedAtMS == 0 || q.ExpiresAtMS <= q.ObservedAtMS || q.ExpiresAtMS-q.ObservedAtMS > MaximumQuoteLifetimeMS ||
		q.Spot.ObservedAtMS != q.ObservedAtMS || q.Perp.ObservedAtMS != q.ObservedAtMS {
		return errors.New("quote lifetime is invalid")
	}
	return nil
}

func IsAllowedUnwindTargetStrategyManifest(value string) bool {
	return value == StrategyManifestSHA256 ||
		value == PreviousStrategyManifestSHA256 ||
		value == LegacyStrategyManifestSHA256
}

func (q QuoteBundle) validExitAuthority() bool {
	authority := q.ExitAuthority
	if authority == nil || authority.Source != "execution-authority" ||
		authority.ExecutionAccountID != q.ExecutionAccountID || authority.MarketManifest != q.MarketManifest ||
		!validHash(authority.IntentID) || !validDigest(authority.PayloadSHA256) ||
		!validSourcePart(authority.SourceSession, 128) || !validSourcePart(authority.SourceEventID, 256) ||
		authority.SourceSequence < 0 || authority.ReceivedAtMS < q.ObservedAtMS || authority.ReceivedAtMS >= q.ExpiresAtMS ||
		authority.SubmissionDeadlineMS != q.ExpiresAtMS ||
		authority.ReconciliationDeadlineMS <= authority.SubmissionDeadlineMS ||
		authority.ReconciliationDeadlineMS-authority.SubmissionDeadlineMS > MaximumExitReconciliationMS {
		return false
	}
	return true
}

func positiveDecimal(value string) (*big.Int, bool) {
	if value == "" || value[0] == '+' || (len(value) > 1 && value[0] == '0') {
		return nil, false
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	return parsed, ok && parsed.Sign() > 0
}

func (q QuoteBundle) calculateID() (string, error) {
	material := q
	material.ID = ""
	material.Signature = ""
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("encode quote id: %w", err)
	}
	return domainHash(quoteIDDomain, encoded), nil
}

func (q QuoteBundle) signatureMaterial() ([]byte, error) {
	material := q
	material.Signature = ""
	encoded, err := json.Marshal(material)
	if err != nil {
		return nil, fmt.Errorf("encode quote signature: %w", err)
	}
	return append(append([]byte{}, quoteSignatureDomain...), encoded...), nil
}

func domainHash(domain, payload []byte) string {
	digest := sha256.New()
	_, _ = digest.Write(domain)
	_, _ = digest.Write(payload)
	return "0x" + hex.EncodeToString(digest.Sum(nil))
}

func validHash(value string) bool {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") || value == "0x"+strings.Repeat("0", 64) {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil && value == strings.ToLower(value)
}

func validExecutionID(value string) bool {
	if len(value) < 8 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	if len(value) != 64 || value == strings.Repeat("0", 64) {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validSourcePart(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}
