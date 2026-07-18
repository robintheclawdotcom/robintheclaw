package evaluation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"
	"time"

	scheduler "github.com/robin-the-claw/live-scheduler"
)

const (
	SourceStrategyVersion = "basis-paper-v1"
	Direction             = "long_spot_short_perp"
	Symbol                = "AAPL"
	stockToken            = "0xaf3d76f1834a1d425780943c99ea8a608f8a93f9"
	quoterCodeHash        = "0xd707b1da8cb165e5ea35a3b4450d971eb562ec171e23492aa117036b78a868f6"
	poolManagerCodeHash   = "0xbd3881180b547f5fe817545743cfb4343e96b1bc6640dcd70c106b0066e95626"
	settlementCodeHash    = "0x864cc9ad53b338b82da1f7cab85ab0b3d5c8861acb422b6fec63cf36234f36a6"
	stockCodeHash         = "0x6c1fdd40002dcb440c7fff6a84171404d279ccb057803b65826f7546acd65630"
	entryNotionalMicros   = uint64(25_000_000)
	dailyTurnoverMicros   = uint64(50_000_000)
	maximumLiveOrderIndex = uint64(1<<40 - 1)
)

var (
	hashPattern       = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
	identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,63}$`)
	decimalPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	uuidPattern       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type PaperEvidence struct {
	Direction             string    `json:"direction"`
	TickerSourceSession   string    `json:"tickerSourceSession"`
	TickerSourceEventID   string    `json:"tickerSourceEventId"`
	TickerTimestampMS     *int64    `json:"tickerTimestampMs"`
	TickerReceivedAt      time.Time `json:"tickerReceivedAt"`
	PerpBidPrice          string    `json:"perpBidPrice"`
	PerpBidSize           string    `json:"perpBidSize"`
	PerpPriceMicros       string    `json:"perpPriceMicros"`
	PerpBidSizeSharesWei  string    `json:"perpBidSizeSharesWei"`
	SettlementAmountInRaw string    `json:"settlementAmountInRaw"`
	StockAmountOutRaw     string    `json:"stockAmountOutRaw"`
	UnderlyingSharesWei   string    `json:"underlyingSharesWei"`
	SettlementDecimals    uint8     `json:"settlementDecimals"`
	StockDecimals         uint8     `json:"stockDecimals"`
	UIMultiplierRaw       string    `json:"uiMultiplierRaw"`
	NewUIMultiplierRaw    string    `json:"newUiMultiplierRaw"`
	EffectiveAt           string    `json:"effectiveAt"`
	OraclePaused          bool      `json:"oraclePaused"`
	SpotPriceMicros       string    `json:"spotPriceMicros"`
	QuoterGas             string    `json:"quoterGas"`
	ExitAmountOutRaw      string    `json:"exitAmountOutRaw"`
	ExitQuoterGas         string    `json:"exitQuoterGas"`
	BlockTimestamp        uint64    `json:"blockTimestamp"`
	QuoterCodeHash        string    `json:"quoterCodeHash"`
	PoolManagerCodeHash   string    `json:"poolManagerCodeHash"`
	SettlementCodeHash    string    `json:"settlementCodeHash"`
	StockCodeHash         string    `json:"stockCodeHash"`
}

type PaperCandidate struct {
	EvaluationID  string
	EventID       string
	EpisodeID     string
	SourceSession string
	SourceEventID string
	Symbol        string
	Status        string
	Reason        *string
	Direction     string
	BlockNumber   uint64
	BlockHash     string
	GrossEdgePPM  uint64
	NetEdgePPM    uint64
	Evidence      PaperEvidence
	EvaluatedAt   time.Time
}

type PaperExit struct {
	EvaluationID  string
	EventID       string
	EpisodeID     string
	SourceSession string
	SourceEventID string
	Symbol        string
	Status        string
	Reason        string
	BlockNumber   uint64
	BlockHash     string
	Evidence      PaperEvidence
	EvaluatedAt   time.Time
	ClosedAt      time.Time
}

type ProductAccount struct {
	ExecutionAccountID string
	AgentID            string
	Lifecycle          string
	AccountStatus      string
	StrategyVersion    string
	StrategyManifest   string
	LighterAccount     uint64
	LighterAPIKey      uint8
	RobinhoodOwner     string
	RobinhoodVault     string
	RobinhoodSigner    string
	BindingSHA256      string
	RegistrationStatus string
	LighterLinked      bool
	LighterFunded      bool
	RobinhoodDeployed  bool
	RobinhoodFunded    bool
	UserGasReady       bool
	ExecutionGasReady  bool
	PolicyActive       bool
	Reconciled         bool
	ObservedAt         time.Time
	ValidUntil         time.Time
}

type MarketConfig struct {
	ManifestID                 string
	Symbol                     string
	SpotToken                  string
	LighterMarketIndex         uint32
	SpotDecimals               uint8
	PerpBaseDecimals           uint8
	PerpPriceDecimals          uint8
	SpotConfigVersion          uint64
	UIMultiplierE18            string
	MaxPriceDeviationBPS       uint16
	MaxSpotSlippageBPS         uint16
	MaxUnwindPriceDeviationBPS uint16
	ReviewRecordSHA256         string
	ValidFrom                  time.Time
	ValidUntil                 time.Time
}

type Approval struct {
	Evaluation scheduler.SourceEvaluation
	Readiness  scheduler.Readiness
	State      scheduler.AccountState
	ExpiresAt  time.Time
}

func DecodePaperEvidence(body []byte) (PaperEvidence, error) {
	var evidence PaperEvidence
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return PaperEvidence{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return PaperEvidence{}, errors.New("paper evidence has trailing JSON")
	}
	return evidence, nil
}

func (candidate PaperCandidate) Validate(now time.Time, minimumNetEdgePPM uint64) error {
	if candidate.EvaluationID == "" || candidate.EventID == "" || candidate.EpisodeID == "" ||
		candidate.SourceSession == "" || candidate.SourceEventID == "" ||
		candidate.Symbol != Symbol || candidate.Status != "candidate" || candidate.Reason != nil ||
		candidate.Direction != Direction || candidate.BlockNumber == 0 || !hashPattern.MatchString(candidate.BlockHash) ||
		candidate.GrossEdgePPM < candidate.NetEdgePPM || candidate.NetEdgePPM < minimumNetEdgePPM {
		return errors.New("paper candidate does not satisfy the live policy")
	}
	if stale(candidate.EvaluatedAt, now) || stale(candidate.Evidence.TickerReceivedAt, now) {
		return errors.New("paper candidate is stale")
	}
	evidence := candidate.Evidence
	if evidence.Direction != Direction || evidence.TickerSourceSession != candidate.SourceSession ||
		evidence.TickerSourceEventID != candidate.SourceEventID || evidence.TickerTimestampMS == nil ||
		*evidence.TickerTimestampMS > now.UnixMilli() || now.UnixMilli()-*evidence.TickerTimestampMS > 3_000 ||
		evidence.SettlementDecimals != 6 || evidence.StockDecimals != 18 || evidence.OraclePaused ||
		evidence.UIMultiplierRaw == "0" || evidence.UIMultiplierRaw != evidence.NewUIMultiplierRaw ||
		evidence.BlockTimestamp == 0 || evidence.QuoterCodeHash != quoterCodeHash ||
		evidence.PoolManagerCodeHash != poolManagerCodeHash || evidence.SettlementCodeHash != settlementCodeHash ||
		evidence.StockCodeHash != stockCodeHash {
		return errors.New("paper evidence does not match the reviewed live source")
	}
	for _, value := range []string{
		evidence.PerpPriceMicros, evidence.PerpBidSizeSharesWei, evidence.SettlementAmountInRaw,
		evidence.StockAmountOutRaw, evidence.UnderlyingSharesWei, evidence.UIMultiplierRaw,
		evidence.NewUIMultiplierRaw, evidence.SpotPriceMicros, evidence.QuoterGas,
		evidence.ExitAmountOutRaw, evidence.ExitQuoterGas,
	} {
		if !positiveInteger(value) {
			return errors.New("paper evidence contains an invalid integer")
		}
	}
	if evidence.SettlementAmountInRaw != "25000000" {
		return errors.New("paper evidence does not use the fixed entry notional")
	}
	return nil
}

func (exit PaperExit) Validate(now time.Time) error {
	if exit.EvaluationID == "" || exit.EventID == "" || !uuidPattern.MatchString(exit.EpisodeID) ||
		exit.SourceSession == "" || exit.SourceEventID == "" || exit.Symbol != Symbol ||
		exit.Status != "declined" || exit.Reason == "" || len(exit.Reason) > 128 || exit.BlockNumber == 0 ||
		!hashPattern.MatchString(exit.BlockHash) || !exit.ClosedAt.Equal(exit.EvaluatedAt) ||
		stale(exit.EvaluatedAt, now) || stale(exit.Evidence.TickerReceivedAt, now) {
		return errors.New("paper exit does not satisfy the live policy")
	}
	evidence := exit.Evidence
	if evidence.Direction != Direction || evidence.TickerSourceSession != exit.SourceSession ||
		evidence.TickerSourceEventID != exit.SourceEventID || evidence.TickerTimestampMS == nil ||
		*evidence.TickerTimestampMS > now.UnixMilli() || now.UnixMilli()-*evidence.TickerTimestampMS > 3_000 ||
		evidence.SettlementDecimals != 6 || evidence.StockDecimals != 18 || evidence.OraclePaused ||
		evidence.UIMultiplierRaw == "0" || evidence.UIMultiplierRaw != evidence.NewUIMultiplierRaw ||
		evidence.BlockTimestamp == 0 || evidence.QuoterCodeHash != quoterCodeHash ||
		evidence.PoolManagerCodeHash != poolManagerCodeHash || evidence.SettlementCodeHash != settlementCodeHash ||
		evidence.StockCodeHash != stockCodeHash || !positiveInteger(evidence.ExitAmountOutRaw) ||
		!positiveInteger(evidence.ExitQuoterGas) {
		return errors.New("paper exit evidence does not match the reviewed live source")
	}
	return nil
}

func (account ProductAccount) Validate(now time.Time) error {
	if !identifierPattern.MatchString(account.ExecutionAccountID) || !identifierPattern.MatchString(account.AgentID) ||
		account.Lifecycle != "running" || account.AccountStatus != "ready" ||
		account.StrategyVersion != scheduler.StrategyVersion || account.StrategyManifest != scheduler.StrategyManifestSHA256 ||
		account.RegistrationStatus != "registered" || account.LighterAccount == 0 || account.LighterAPIKey < 4 ||
		!validAddress(account.RobinhoodOwner) || !validAddress(account.RobinhoodVault) || !validAddress(account.RobinhoodSigner) ||
		!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(account.BindingSHA256) {
		return errors.New("product account binding is invalid")
	}
	if account.RobinhoodOwner == account.RobinhoodVault || account.RobinhoodOwner == account.RobinhoodSigner ||
		account.RobinhoodVault == account.RobinhoodSigner || !account.LighterLinked || !account.LighterFunded ||
		!account.RobinhoodDeployed || !account.RobinhoodFunded || !account.UserGasReady ||
		!account.ExecutionGasReady || !account.PolicyActive || !account.Reconciled ||
		stale(account.ObservedAt, now) || !now.Before(account.ValidUntil) {
		return errors.New("product account readiness is not live")
	}
	return nil
}

func DatasetManifest(candidate PaperCandidate) (string, error) {
	evidence, err := json.Marshal(candidate.Evidence)
	if err != nil {
		return "", err
	}
	evidenceDigest := sha256.Sum256(evidence)
	return domainHash("robin.live.dataset-manifest.v1", struct {
		SourceStrategyVersion string `json:"source_strategy_version"`
		EvaluationID          string `json:"evaluation_id"`
		EventID               string `json:"event_id"`
		EpisodeID             string `json:"episode_id"`
		SourceSession         string `json:"source_session"`
		SourceEventID         string `json:"source_event_id"`
		BlockNumber           uint64 `json:"block_number"`
		BlockHash             string `json:"block_hash"`
		EvidenceSHA256        string `json:"evidence_sha256"`
	}{SourceStrategyVersion, candidate.EvaluationID, candidate.EventID, candidate.EpisodeID,
		candidate.SourceSession, candidate.SourceEventID,
		candidate.BlockNumber, candidate.BlockHash, hex.EncodeToString(evidenceDigest[:])})
}

func ExitDatasetManifest(exit PaperExit) (string, error) {
	evidence, err := json.Marshal(exit.Evidence)
	if err != nil {
		return "", err
	}
	evidenceDigest := sha256.Sum256(evidence)
	return domainHash("robin.live.exit-dataset-manifest.v1", struct {
		SourceStrategyVersion string `json:"source_strategy_version"`
		EvaluationID          string `json:"evaluation_id"`
		EventID               string `json:"event_id"`
		EpisodeID             string `json:"episode_id"`
		SourceSession         string `json:"source_session"`
		SourceEventID         string `json:"source_event_id"`
		CloseReason           string `json:"close_reason"`
		BlockNumber           uint64 `json:"block_number"`
		BlockHash             string `json:"block_hash"`
		EvidenceSHA256        string `json:"evidence_sha256"`
	}{SourceStrategyVersion, exit.EvaluationID, exit.EventID, exit.EpisodeID,
		exit.SourceSession, exit.SourceEventID, exit.Reason, exit.BlockNumber,
		exit.BlockHash, hex.EncodeToString(evidenceDigest[:])})
}

func MarketManifest(config MarketConfig) (string, error) {
	return domainHash("robin.live.market-manifest.v1", struct {
		Symbol                     string `json:"symbol"`
		SpotToken                  string `json:"spot_token"`
		LighterMarketIndex         uint32 `json:"lighter_market_index"`
		SpotDecimals               uint8  `json:"spot_decimals"`
		PerpBaseDecimals           uint8  `json:"perp_base_decimals"`
		PerpPriceDecimals          uint8  `json:"perp_price_decimals"`
		SpotConfigVersion          uint64 `json:"spot_config_version"`
		UIMultiplierE18            string `json:"ui_multiplier_e18"`
		MaxPriceDeviationBPS       uint16 `json:"max_price_deviation_bps"`
		MaxSpotSlippageBPS         uint16 `json:"max_spot_slippage_bps"`
		MaxUnwindPriceDeviationBPS uint16 `json:"max_unwind_price_deviation_bps"`
		ReviewRecordSHA256         string `json:"review_record_sha256"`
		ValidFromMS                int64  `json:"valid_from_ms"`
		ValidUntilMS               int64  `json:"valid_until_ms"`
	}{config.Symbol, config.SpotToken, config.LighterMarketIndex, config.SpotDecimals,
		config.PerpBaseDecimals, config.PerpPriceDecimals, config.SpotConfigVersion,
		config.UIMultiplierE18, config.MaxPriceDeviationBPS, config.MaxSpotSlippageBPS,
		config.MaxUnwindPriceDeviationBPS, config.ReviewRecordSHA256,
		config.ValidFrom.UnixMilli(), config.ValidUntil.UnixMilli()})
}

func SourceEvaluationID(candidate PaperCandidate, datasetManifest, marketManifest string) (string, error) {
	return domainHash("robin.live.source-evaluation.v1", struct {
		SourceEvaluationID     string `json:"source_evaluation_id"`
		DatasetManifest        string `json:"dataset_manifest"`
		MarketManifest         string `json:"market_manifest"`
		StrategyVersion        string `json:"strategy_version"`
		StrategyManifestSHA256 string `json:"strategy_manifest_sha256"`
		SourceConfigSHA256     string `json:"source_config_sha256"`
	}{candidate.EvaluationID, datasetManifest, marketManifest, scheduler.StrategyVersion,
		scheduler.StrategyManifestSHA256, scheduler.SourceConfigSHA256})
}

func ExitEvaluationID(exit PaperExit, datasetManifest, marketManifest, intentID string) (string, error) {
	if !hashPattern.MatchString(intentID) {
		return "", errors.New("exit intent binding is invalid")
	}
	return domainHash("robin.live.source-exit.v1", struct {
		PaperEvaluationID      string `json:"paper_evaluation_id"`
		SourceEpisodeID        string `json:"source_episode_id"`
		PairIntentID           string `json:"pair_intent_id"`
		DatasetManifest        string `json:"dataset_manifest"`
		MarketManifest         string `json:"market_manifest"`
		StrategyVersion        string `json:"strategy_version"`
		StrategyManifestSHA256 string `json:"strategy_manifest_sha256"`
		SourceConfigSHA256     string `json:"source_config_sha256"`
	}{exit.EvaluationID, exit.EpisodeID, intentID, datasetManifest, marketManifest,
		scheduler.StrategyVersion, scheduler.StrategyManifestSHA256, scheduler.SourceConfigSHA256})
}

func EstimatedCostMicros(candidate PaperCandidate) (uint64, error) {
	if candidate.GrossEdgePPM < candidate.NetEdgePPM {
		return 0, errors.New("net edge exceeds gross edge")
	}
	costPPM := candidate.GrossEdgePPM - candidate.NetEdgePPM
	product := new(big.Int).Mul(new(big.Int).SetUint64(entryNotionalMicros), new(big.Int).SetUint64(costPPM))
	product.Add(product, big.NewInt(999_999)).Quo(product, big.NewInt(1_000_000))
	if !product.IsUint64() {
		return 0, errors.New("estimated cost is out of range")
	}
	return product.Uint64(), nil
}

func domainHash(domain string, value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(body)
	return "0x" + hex.EncodeToString(hash.Sum(nil)), nil
}

func stale(observed, now time.Time) bool {
	age := now.Sub(observed)
	return observed.IsZero() || age < 0 || age > 5*time.Second
}

func positiveInteger(value string) bool {
	return decimalPattern.MatchString(value) && value != "0"
}

func validAddress(value string) bool {
	if len(value) != 42 || !strings.HasPrefix(value, "0x") || value == "0x"+strings.Repeat("0", 40) {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil && value == strings.ToLower(value)
}

func validHash(value string) bool {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") || value == "0x"+strings.Repeat("0", 64) {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil && value == strings.ToLower(value)
}

func validateMarket(config MarketConfig, now time.Time, lighterMarket uint32) error {
	manifest, err := MarketManifest(config)
	if err != nil {
		return err
	}
	if config.ManifestID != manifest || config.Symbol != Symbol || config.SpotToken != stockToken ||
		config.LighterMarketIndex != lighterMarket || config.SpotDecimals != 18 || config.PerpBaseDecimals > 18 ||
		config.PerpPriceDecimals > 18 || config.SpotConfigVersion == 0 || !positiveInteger(config.UIMultiplierE18) ||
		!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(config.ReviewRecordSHA256) ||
		now.Before(config.ValidFrom) || !now.Before(config.ValidUntil) {
		return fmt.Errorf("market config does not match the reviewed AAPL policy")
	}
	return nil
}
