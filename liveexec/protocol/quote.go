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
	StrategyVersion        = "basis-aapl-v1"
	StrategyManifestSHA256 = "4d89928827e929a1991f3d47d31acf6a609ed9a9f84212b7ab780e3daecf8e0a"
	SourceConfigSHA256     = "b701b39cbce20ccef48527811299732812d14297750fc3eee2a3c4a4a3f29edd"
	RouteSHA256            = "23559b51e5512cfa0ab21ceeb3fbf97fc0edf3993528ae7b68d40affec6df5c8"
	OraclePolicySHA256     = "b6f928e078847713aaca6c308769a774f367ec89f5f02d7332e1989095e53578"
	RiskPolicySHA256       = "b6a73ad263d6b61fabda029282410dc8200e700c956d2804508b354bbfeb94f6"
	RiskVersion            = StrategyVersion
	ChainID                = uint64(4663)
	Symbol                 = "AAPL"
	SpotVenue              = "robinhood-chain-mainnet"
	PerpVenue              = "lighter-mainnet"
	SettlementToken        = "0x5fc5360d0400a0fd4f2af552add042d716f1d168"
	StockToken             = "0xaf3d76f1834a1d425780943c99ea8a608f8a93f9"
	Router                 = "0x8876789976decbfcbbbe364623c63652db8c0904"
	EntryNotionalMicros    = uint64(25_000_000)
	MaximumQuoteLifetimeMS = uint64(5_000)
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
	RequestID          string `json:"request_id"`
	ExecutionAccountID string `json:"execution_account_id"`
	SourceEvaluationID string `json:"source_evaluation_id"`
	Action             Action `json:"action"`
	RequestedAtMS      uint64 `json:"requested_at_ms"`
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
	BaseAmount    uint64 `json:"base_amount"`
	BaseDecimals  uint8  `json:"base_decimals"`
	PriceDecimals uint8  `json:"price_decimals"`
	LimitPrice    uint32 `json:"limit_price"`
	MarkPrice     uint32 `json:"mark_price"`
	ObservedAtMS  uint64 `json:"observed_at_ms"`
}

type QuoteBundle struct {
	SchemaVersion          uint8          `json:"schema_version"`
	ID                     string         `json:"id"`
	RequestID              string         `json:"request_id"`
	ExecutionAccountID     string         `json:"execution_account_id"`
	SourceEvaluationID     string         `json:"source_evaluation_id"`
	StrategyVersion        string         `json:"strategy_version"`
	StrategyManifestSHA256 string         `json:"strategy_manifest_sha256"`
	SourceConfigSHA256     string         `json:"source_config_sha256"`
	RouteSHA256            string         `json:"route_sha256"`
	OraclePolicySHA256     string         `json:"oracle_policy_sha256"`
	RiskPolicySHA256       string         `json:"risk_policy_sha256"`
	Action                 Action         `json:"action"`
	Source                 SourceIdentity `json:"source"`
	Spot                   SpotQuote      `json:"spot"`
	Perp                   PerpQuote      `json:"perp"`
	ObservedAtMS           uint64         `json:"observed_at_ms"`
	ExpiresAtMS            uint64         `json:"expires_at_ms"`
	PublicKey              string         `json:"public_key"`
	Signature              string         `json:"signature"`
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

func (q QuoteBundle) Verify(expectedPublicKey ed25519.PublicKey, nowMS uint64) error {
	if len(expectedPublicKey) != ed25519.PublicKeySize {
		return errors.New("invalid trusted quote key")
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
	if err := q.validateCanonical(); err != nil {
		return err
	}
	if q.ObservedAtMS > nowMS || q.ExpiresAtMS <= nowMS || nowMS-q.ObservedAtMS > MaximumQuoteLifetimeMS {
		return errors.New("quote is stale")
	}
	return nil
}

func (q QuoteBundle) validateCanonical() error {
	if q.SchemaVersion != 1 || q.StrategyVersion != StrategyVersion || q.StrategyManifestSHA256 != StrategyManifestSHA256 ||
		q.SourceConfigSHA256 != SourceConfigSHA256 || q.RouteSHA256 != RouteSHA256 ||
		q.OraclePolicySHA256 != OraclePolicySHA256 || q.RiskPolicySHA256 != RiskPolicySHA256 {
		return errors.New("quote policy mismatch")
	}
	if !validHash(q.ID) || !validHash(q.RequestID) || !validHash(q.SourceEvaluationID) || !validHash(q.Spot.BlockHash) {
		return errors.New("quote identity is invalid")
	}
	if !validExecutionID(q.ExecutionAccountID) || q.Source.AdapterID == "" || q.Source.SpotSource == "" ||
		q.Source.PerpSource == "" || q.Source.OracleRound == "" {
		return errors.New("quote source is incomplete")
	}
	if q.Action != ActionEntry && q.Action != ActionUnwind {
		return errors.New("quote action is invalid")
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
	if q.Spot.SettlementAmount == "" || q.Spot.StockAmount == "" || q.Spot.MinimumAmountOut == "" ||
		q.Spot.ReferencePriceMicros == 0 || q.Perp.BaseAmount == 0 || q.Perp.LimitPrice == 0 || q.Perp.MarkPrice == 0 ||
		q.Perp.MarketIndex > 32767 || q.Perp.BaseDecimals > 18 || q.Perp.PriceDecimals > 18 {
		return errors.New("quote amounts are invalid")
	}
	settlement, settlementOK := positiveDecimal(q.Spot.SettlementAmount)
	stock, stockOK := positiveDecimal(q.Spot.StockAmount)
	minimum, minimumOK := positiveDecimal(q.Spot.MinimumAmountOut)
	if !settlementOK || !stockOK || !minimumOK ||
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
