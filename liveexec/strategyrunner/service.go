package strategyrunner

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

const (
	minimumUnwindSettlementMicros = uint64(24_000_000)
	maximumDailyTurnoverMicros    = uint64(50_000_000)
	maximumClientOrderIndex       = uint64(1<<48 - 1)
	maximumEvidenceAgeMS          = uint64(5_000)
	maximumUnwindAttempts         = uint8(3)
)

var (
	pairIntentDomain      = []byte("robin.execution.pair-intent.v2\x00")
	spotUnwindDomain      = []byte("robin.execution.spot-unwind.v2\x00")
	unwindDirectiveDomain = []byte("robin.live.unwind-directive.v1\x00")
)

type Service struct {
	quotePublicKey ed25519.PublicKey
	now            func() time.Time
}

func NewService(quotePublicKey ed25519.PublicKey) (*Service, error) {
	if len(quotePublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("trusted quote public key is required")
	}
	return &Service{quotePublicKey: append(ed25519.PublicKey(nil), quotePublicKey...), now: time.Now}, nil
}

func (s *Service) Run(input RunRequest) (RunOutput, error) {
	nowMS := uint64(s.now().UnixMilli())
	if err := input.Quotes.Verify(s.quotePublicKey, nowMS); err != nil {
		return RunOutput{}, err
	}
	if err := validateEvidence(input, nowMS); err != nil {
		return RunOutput{}, err
	}
	switch input.Evaluation.Action {
	case protocol.ActionEntry:
		intent, err := buildEntry(input, nowMS)
		if err != nil {
			return RunOutput{}, err
		}
		return RunOutput{Kind: protocol.ActionEntry, PairIntent: &intent}, nil
	case protocol.ActionUnwind:
		directive, err := buildUnwind(input)
		if err != nil {
			return RunOutput{}, err
		}
		return RunOutput{Kind: protocol.ActionUnwind, Unwind: &directive}, nil
	default:
		return RunOutput{}, errors.New("unsupported strategy action")
	}
}

func validateEvidence(input RunRequest, nowMS uint64) error {
	evaluation := input.Evaluation
	readiness := input.Readiness
	state := input.AccountState
	quotes := input.Quotes
	if evaluation.StrategyVersion != protocol.StrategyVersion || evaluation.StrategyManifestSHA256 != protocol.StrategyManifestSHA256 ||
		evaluation.SourceConfigSHA256 != protocol.SourceConfigSHA256 || evaluation.Status != "approved" ||
		!validHash(evaluation.ID) || !validHash(evaluation.DatasetManifest) || !validHash(evaluation.MarketManifest) {
		return errors.New("source evaluation is not an approved canonical evaluation")
	}
	if !fresh(evaluation.ObservedAtMS, nowMS) || !fresh(readiness.ObservedAtMS, nowMS) || !fresh(state.ObservedAtMS, nowMS) {
		return errors.New("strategy evidence is stale")
	}
	if quotes.SourceEvaluationID != evaluation.ID || quotes.Action != evaluation.Action ||
		quotes.ExecutionAccountID != readiness.ExecutionAccountID || quotes.ExecutionAccountID != state.ExecutionAccountID ||
		readiness.ExecutionAccountID != state.ExecutionAccountID || readiness.AgentID != state.AgentID ||
		readiness.StrategyVersion != protocol.StrategyVersion || readiness.StrategyManifestSHA256 != protocol.StrategyManifestSHA256 ||
		state.StrategyManifestSHA256 != protocol.StrategyManifestSHA256 {
		return errors.New("strategy evidence identity mismatch")
	}
	if !validExecutionID(state.ExecutionAccountID) || !validExecutionID(state.AgentID) ||
		!validAddress(state.RobinhoodVault) || !validAddress(state.RobinhoodSigner) || state.RobinhoodVault == state.RobinhoodSigner ||
		state.LighterAccountIndex == 0 || state.LighterAPIKeyIndex < 2 || state.LighterAPIKeyIndex > 254 ||
		state.LighterMarketIndex != quotes.Perp.MarketIndex || state.SpotConfigVersion == 0 ||
		state.NextClientOrderIndex > maximumClientOrderIndex || state.NextUnwindOrderIndex > maximumClientOrderIndex ||
		state.NextClientOrderIndex == state.NextUnwindOrderIndex {
		return errors.New("execution account binding is invalid")
	}
	if !readiness.FullyVerified || !readiness.VaultWired || !readiness.VaultFunded || !readiness.ExecutionSignerFunded ||
		!readiness.LighterLinked || !readiness.LighterFunded || !readiness.RouteHealthy || !readiness.OracleHealthy ||
		!readiness.SequencerHealthy || !state.LighterNonceAligned || !state.RobinhoodNonceAligned ||
		state.UnknownLighterOrders || state.UnknownLighterPositions || state.UnknownRobinhoodPosition {
		return errors.New("execution account is not ready")
	}
	if evaluation.Action == protocol.ActionEntry {
		if readiness.Lifecycle != "ready" && readiness.Lifecycle != "running" {
			return errors.New("entry lifecycle is not ready")
		}
		if readiness.GlobalControl != "active" || readiness.StrategyControl != "active" || readiness.AccountControl != "active" {
			return errors.New("entry controls are not active")
		}
		if !state.Flat || state.ActiveEpisodes != 0 || state.NAVMicros == 0 || state.DailyTurnoverMicros > maximumDailyTurnoverMicros-2*protocol.EntryNotionalMicros {
			return errors.New("entry account limits are not available")
		}
		if state.MaintenanceMarginMicros == 0 || state.MaintenanceMarginMicros > state.CollateralMicros/2 {
			return errors.New("maintenance margin coverage is below 2x")
		}
	} else {
		if readiness.Lifecycle != "running" && readiness.Lifecycle != "reducing" && readiness.Lifecycle != "closing" {
			return errors.New("unwind lifecycle is invalid")
		}
		if !restrictiveOrActive(readiness.GlobalControl) || !restrictiveOrActive(readiness.StrategyControl) || !restrictiveOrActive(readiness.AccountControl) {
			return errors.New("unwind controls are invalid")
		}
		if state.Flat || state.ActiveEpisodes != 1 {
			return errors.New("no single active episode to unwind")
		}
	}
	return nil
}

func buildEntry(input RunRequest, nowMS uint64) (PairIntent, error) {
	quotes := input.Quotes
	state := input.AccountState
	settlement, ok := positiveDecimalUint64(quotes.Spot.SettlementAmount)
	if !ok || settlement != protocol.EntryNotionalMicros || quotes.Spot.Side != "buy" || quotes.Perp.Side != "short" || quotes.Perp.ReduceOnly {
		return PairIntent{}, errors.New("entry quote is not the fixed entry")
	}
	perpNotional, ok := derivedPerpNotional(quotes.Perp)
	if !ok || perpNotional != protocol.EntryNotionalMicros {
		return PairIntent{}, errors.New("entry perp notional mismatch")
	}
	if err := validateExposure(quotes.Spot.StockAmount, state.UIMultiplierE18, state.SpotDecimals, quotes.Perp.BaseAmount, quotes.Perp.BaseDecimals); err != nil {
		return PairIntent{}, err
	}
	unwindPrice := uint64(quotes.Perp.MarkPrice) * 120 / 100
	if unwindPrice == 0 || unwindPrice > uint64(^uint32(0)) {
		return PairIntent{}, errors.New("unwind price is not representable")
	}
	if state.NextUnwindOrderIndex+uint64(maximumUnwindAttempts)-1 > maximumClientOrderIndex {
		return PairIntent{}, errors.New("unwind order range is not available")
	}
	intent := PairIntent{
		Version:                    2,
		ExecutionAccountID:         state.ExecutionAccountID,
		AgentID:                    state.AgentID,
		SourceEvaluationID:         input.Evaluation.ID,
		RiskVersion:                protocol.RiskVersion,
		StrategyManifestSHA256:     protocol.StrategyManifestSHA256,
		LighterAccountIndex:        state.LighterAccountIndex,
		LighterAPIKeyIndex:         state.LighterAPIKeyIndex,
		RobinhoodVault:             state.RobinhoodVault,
		RobinhoodSigner:            state.RobinhoodSigner,
		Symbol:                     protocol.Symbol,
		SpotToken:                  protocol.StockToken,
		LighterMarketIndex:         state.LighterMarketIndex,
		SpotSide:                   "buy",
		PerpSide:                   "short",
		SpotNotionalMicros:         settlement,
		PerpNotionalMicros:         perpNotional,
		NAVMicros:                  state.NAVMicros,
		RawSpotAmount:              quotes.Spot.StockAmount,
		SettlementAmountIn:         quotes.Spot.SettlementAmount,
		MinimumSpotAmountOut:       quotes.Spot.MinimumAmountOut,
		MinimumUnwindSettlementOut: uintString(minimumUnwindSettlementMicros),
		SpotDecimals:               state.SpotDecimals,
		SpotConfigVersion:          state.SpotConfigVersion,
		PerpBaseAmount:             quotes.Perp.BaseAmount,
		PerpBaseDecimals:           quotes.Perp.BaseDecimals,
		PerpPriceDecimals:          quotes.Perp.PriceDecimals,
		PerpLimitPrice:             quotes.Perp.LimitPrice,
		ClientOrderIndex:           state.NextClientOrderIndex,
		PerpUnwindPrice:            uint32(unwindPrice),
		UnwindClientOrderIndex:     state.NextUnwindOrderIndex,
		MaxUnwindAttempts:          maximumUnwindAttempts,
		PerpOrderExpiryMS:          nowMS + 300_000,
		EmergencyDeadlineMS:        nowMS + 600_000,
		ReconciliationDeadlineMS:   nowMS + 86_400_000,
		LeverageMicros:             1_000_000,
		CreatedAtMS:                nowMS,
		DeadlineMS:                 quotes.ExpiresAtMS,
		Evidence: FrozenEvidence{
			DatasetManifest:          input.Evaluation.DatasetManifest,
			StrategyVersion:          protocol.StrategyVersion,
			MarketManifest:           input.Evaluation.MarketManifest,
			QuoteBlockHash:           quotes.Spot.BlockHash,
			QuoteReceivedAtMS:        quotes.ObservedAtMS,
			QuoteExpiresAtMS:         quotes.ExpiresAtMS,
			UIMultiplierE18:          state.UIMultiplierE18,
			PerpMarkPrice:            quotes.Perp.MarkPrice,
			EstimatedTotalCostMicros: input.Evaluation.EstimatedCostMicros,
		},
	}
	if intent.DeadlineMS <= intent.CreatedAtMS {
		return PairIntent{}, errors.New("entry quote expired before intent creation")
	}
	if err := intent.deriveIDs(); err != nil {
		return PairIntent{}, err
	}
	return intent, nil
}

func buildUnwind(input RunRequest) (UnwindDirective, error) {
	if input.OpenEpisode == nil {
		return UnwindDirective{}, errors.New("open episode is required")
	}
	episode := input.OpenEpisode
	quotes := input.Quotes
	quoteMinimum, quoteMinimumOK := positiveDecimal(quotes.Spot.MinimumAmountOut)
	episodeMinimum, episodeMinimumOK := positiveDecimal(episode.MinimumSettlementAmountOut)
	if !validHash(episode.PairIntentID) || domainHash(spotUnwindDomain, []byte(episode.PairIntentID)) != episode.SpotUnwindIntentID ||
		episode.SpotAmount != quotes.Spot.StockAmount || episode.PerpBaseAmount != quotes.Perp.BaseAmount ||
		!quoteMinimumOK || !episodeMinimumOK || quoteMinimum.Cmp(episodeMinimum) < 0 ||
		quotes.Spot.Side != "sell" || quotes.Perp.Side != "long" || !quotes.Perp.ReduceOnly {
		return UnwindDirective{}, errors.New("unwind quote does not match the open episode")
	}
	directive := UnwindDirective{
		Version:                    1,
		PairIntentID:               episode.PairIntentID,
		SpotUnwindIntentID:         episode.SpotUnwindIntentID,
		ExecutionAccountID:         input.AccountState.ExecutionAccountID,
		AgentID:                    input.AccountState.AgentID,
		SourceEvaluationID:         input.Evaluation.ID,
		StrategyVersion:            protocol.StrategyVersion,
		StrategyManifestSHA256:     protocol.StrategyManifestSHA256,
		RiskVersion:                protocol.RiskVersion,
		SpotSide:                   "sell",
		SpotAmountIn:               quotes.Spot.StockAmount,
		MinimumSettlementAmountOut: quotes.Spot.MinimumAmountOut,
		PerpSide:                   "long",
		PerpBaseAmount:             quotes.Perp.BaseAmount,
		PerpLimitPrice:             quotes.Perp.LimitPrice,
		ReduceOnly:                 true,
		ObservedAtMS:               quotes.ObservedAtMS,
		DeadlineMS:                 quotes.ExpiresAtMS,
	}
	encoded, err := json.Marshal(directive)
	if err != nil {
		return UnwindDirective{}, err
	}
	directive.ID = domainHash(unwindDirectiveDomain, encoded)
	return directive, nil
}

func (intent *PairIntent) deriveIDs() error {
	material := *intent
	material.ID = ""
	material.SpotUnwindIntentID = ""
	encoded, err := json.Marshal(material)
	if err != nil {
		return err
	}
	intent.ID = domainHash(pairIntentDomain, encoded)
	intent.SpotUnwindIntentID = domainHash(spotUnwindDomain, []byte(intent.ID))
	return nil
}

func validateExposure(stockAmount, multiplier string, spotDecimals uint8, perpBase uint64, perpDecimals uint8) error {
	stock, ok := positiveDecimal(stockAmount)
	if !ok {
		return errors.New("stock amount is invalid")
	}
	multiplierValue, ok := positiveDecimal(multiplier)
	if !ok {
		return errors.New("ui multiplier is invalid")
	}
	adjusted := new(big.Int).Mul(stock, multiplierValue)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	if new(big.Int).Mod(adjusted, scale).Sign() != 0 {
		return errors.New("stock multiplier is not exact")
	}
	adjusted.Div(adjusted, scale)
	if spotDecimals < perpDecimals {
		adjusted.Mul(adjusted, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(perpDecimals-spotDecimals)), nil))
	} else if spotDecimals > perpDecimals {
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(spotDecimals-perpDecimals)), nil)
		if new(big.Int).Mod(adjusted, divisor).Sign() != 0 {
			return errors.New("stock exposure cannot be scaled exactly")
		}
		adjusted.Div(adjusted, divisor)
	}
	if adjusted.Cmp(new(big.Int).SetUint64(perpBase)) != 0 {
		return errors.New("spot and perp share exposure mismatch")
	}
	return nil
}

func derivedPerpNotional(quote protocol.PerpQuote) (uint64, bool) {
	price := quote.LimitPrice
	if quote.MarkPrice > price {
		price = quote.MarkPrice
	}
	numerator := new(big.Int).SetUint64(quote.BaseAmount)
	numerator.Mul(numerator, new(big.Int).SetUint64(uint64(price)))
	numerator.Mul(numerator, big.NewInt(1_000_000))
	denominator := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(quote.BaseDecimals)+int64(quote.PriceDecimals)), nil)
	numerator.Add(numerator, new(big.Int).Sub(denominator, big.NewInt(1)))
	value := numerator.Div(numerator, denominator)
	return value.Uint64(), value.IsUint64()
}

func positiveDecimalUint64(value string) (uint64, bool) {
	parsed, ok := positiveDecimal(value)
	if !ok || !parsed.IsUint64() {
		return 0, false
	}
	return parsed.Uint64(), true
}

func positiveDecimal(value string) (*big.Int, bool) {
	if value == "" || value[0] == '+' || (len(value) > 1 && value[0] == '0') {
		return nil, false
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	return parsed, ok && parsed.Sign() > 0
}

func uintString(value uint64) string {
	return new(big.Int).SetUint64(value).String()
}

func fresh(observedAtMS, nowMS uint64) bool {
	return observedAtMS <= nowMS && nowMS-observedAtMS <= maximumEvidenceAgeMS
}

func restrictiveOrActive(value string) bool {
	return value == "active" || value == "reduce_only"
}

func validHash(value string) bool {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") || value == "0x"+strings.Repeat("0", 64) {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil && value == strings.ToLower(value)
}

func validAddress(value string) bool {
	if len(value) != 42 || !strings.HasPrefix(value, "0x") || value == "0x"+strings.Repeat("0", 40) {
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

func domainHash(domain, payload []byte) string {
	digest := sha256.New()
	_, _ = digest.Write(domain)
	_, _ = digest.Write(payload)
	return "0x" + hex.EncodeToString(digest.Sum(nil))
}
