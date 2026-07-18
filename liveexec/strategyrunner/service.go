package strategyrunner

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

const (
	minimumUnwindSettlementPpm = uint64(960_000)
	maximumDailyTurnoverMicros = uint64(50_000_000)
	maximumClientOrderIndex    = uint64(1<<48 - 1)
	maximumEvidenceAgeMS       = uint64(5_000)
	maximumUnwindAttempts      = uint8(3)
)

var (
	pairIntentDomain      = []byte("robin.execution.pair-intent.v2\x00")
	spotUnwindDomain      = []byte("robin.execution.spot-unwind.v2\x00")
	unwindDirectiveDomain = []byte("robin.live.unwind-directive.v1\x00")
)

type Service struct {
	quotePublicKey     ed25519.PublicKey
	dispatcher         IntentDispatcher
	lighterMarketIndex uint32
	now                func() time.Time
}

func NewService(quotePublicKey ed25519.PublicKey, dispatcher IntentDispatcher, lighterMarketIndex uint32) (*Service, error) {
	if len(quotePublicKey) != ed25519.PublicKeySize || dispatcher == nil || lighterMarketIndex > 32767 {
		return nil, errors.New("trusted quote public key, reviewed market index, and coordinator dispatcher are required")
	}
	return &Service{
		quotePublicKey: append(ed25519.PublicKey(nil), quotePublicKey...), dispatcher: dispatcher,
		lighterMarketIndex: lighterMarketIndex, now: time.Now,
	}, nil
}

func (s *Service) Run(ctx context.Context, input RunRequest) (RunOutput, error) {
	nowMS := uint64(s.now().UnixMilli())
	if err := input.Quotes.Verify(s.quotePublicKey, s.lighterMarketIndex, nowMS); err != nil {
		return RunOutput{}, err
	}
	if err := validateEvidence(input, s.lighterMarketIndex, nowMS); err != nil {
		return RunOutput{}, err
	}
	switch input.Evaluation.Action {
	case protocol.ActionEntry:
		intent, err := buildEntry(input, nowMS)
		if err != nil {
			return RunOutput{}, err
		}
		persistence, err := s.dispatcher.SubmitIntent(ctx, intent)
		if err != nil {
			return RunOutput{}, err
		}
		if persistence.Status != "persisted" || persistence.IntentID != intent.ID ||
			!persistedSagaState(persistence.CoordinatorState) || persistence.CoordinatorVersion == 0 {
			return RunOutput{}, fmt.Errorf("%w: invalid persistence receipt", ErrCoordinatorAmbiguous)
		}
		return RunOutput{Kind: protocol.ActionEntry, PairIntent: &intent, Persistence: &persistence}, nil
	case protocol.ActionUnwind:
		directive, err := buildUnwind(input)
		if err != nil {
			return RunOutput{}, err
		}
		reason, err := exitReason(input.Readiness.Lifecycle)
		if err != nil {
			return RunOutput{}, err
		}
		exit := ExitSubmission{
			RequestID:                  directive.ID,
			ExecutionAccountID:         directive.ExecutionAccountID,
			IntentID:                   directive.PairIntentID,
			QuoteSourceSession:         directive.QuoteSourceSession,
			QuoteSourceEventID:         directive.QuoteSourceEventID,
			QuotePayloadSHA256:         directive.QuotePayloadSHA256,
			PerpUnwindPrice:            directive.PerpLimitPrice,
			MinimumUnwindSettlementOut: directive.MinimumSettlementAmountOut,
			RequestedAtMS:              input.Quotes.ExitAuthority.ReceivedAtMS,
			SubmissionDeadlineMS:       directive.DeadlineMS,
			ReconciliationDeadlineMS:   directive.ReconciliationDeadlineMS,
			Reason:                     reason,
		}
		persistence, err := s.dispatcher.SubmitExit(ctx, exit)
		if err != nil {
			return RunOutput{}, err
		}
		if persistence.Status != "persisted" || persistence.RequestID != directive.ID ||
			persistence.IntentID != directive.PairIntentID || !exitSagaState(persistence.CoordinatorState) ||
			persistence.CoordinatorVersion == 0 {
			return RunOutput{}, fmt.Errorf("%w: invalid exit persistence receipt", ErrCoordinatorAmbiguous)
		}
		return RunOutput{Kind: protocol.ActionUnwind, Unwind: &directive, ExitPersistence: &persistence}, nil
	default:
		return RunOutput{}, errors.New("unsupported strategy action")
	}
}

func validateEvidence(input RunRequest, lighterMarketIndex uint32, nowMS uint64) error {
	evaluation := input.Evaluation
	readiness := input.Readiness
	state := input.AccountState
	quotes := input.Quotes
	if evaluation.StrategyVersion != protocol.StrategyVersion || evaluation.StrategyManifestSHA256 != protocol.StrategyManifestSHA256 ||
		evaluation.SourceConfigSHA256 != protocol.SourceConfigSHA256 || evaluation.Status != "approved" ||
		!validHash(evaluation.ID) || !validHash(evaluation.DatasetManifest) || !validHash(evaluation.MarketManifest) ||
		!validUUID(evaluation.SourceEpisodeID) || !validUUID(evaluation.PaperEvaluationID) ||
		(evaluation.Action == protocol.ActionEntry && evaluation.PairIntentID != "") ||
		(evaluation.Action == protocol.ActionUnwind && (!validHash(evaluation.PairIntentID) || input.OpenEpisode == nil ||
			input.OpenEpisode.PairIntentID != evaluation.PairIntentID)) {
		return errors.New("source evaluation is not an approved canonical evaluation")
	}
	if !fresh(evaluation.ObservedAtMS, nowMS) || !fresh(readiness.ObservedAtMS, nowMS) || !fresh(state.ObservedAtMS, nowMS) {
		return errors.New("strategy evidence is stale")
	}
	if quotes.SourceEvaluationID != evaluation.ID || quotes.Action != evaluation.Action ||
		quotes.MarketManifest != evaluation.MarketManifest ||
		(evaluation.Action == protocol.ActionEntry && quotes.TargetStrategyManifestSHA256 != "") ||
		(evaluation.Action == protocol.ActionUnwind &&
			quotes.TargetStrategyManifestSHA256 != evaluation.StrategyManifestSHA256) ||
		quotes.ExecutionAccountID != readiness.ExecutionAccountID || quotes.ExecutionAccountID != state.ExecutionAccountID ||
		readiness.ExecutionAccountID != state.ExecutionAccountID || readiness.AgentID != state.AgentID ||
		readiness.StrategyVersion != protocol.StrategyVersion || readiness.StrategyManifestSHA256 != protocol.StrategyManifestSHA256 ||
		state.StrategyManifestSHA256 != protocol.StrategyManifestSHA256 {
		return errors.New("strategy evidence identity mismatch")
	}
	if !validExecutionID(state.ExecutionAccountID) || !validExecutionID(state.AgentID) ||
		!validAddress(state.RobinhoodVault) || !validAddress(state.RobinhoodSigner) || state.RobinhoodVault == state.RobinhoodSigner ||
		state.LighterAccountIndex == 0 || state.LighterAPIKeyIndex < 4 || state.LighterAPIKeyIndex > 254 ||
		state.LighterMarketIndex != lighterMarketIndex || quotes.Perp.MarketIndex != lighterMarketIndex || state.SpotConfigVersion == 0 ||
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
		if readiness.GlobalControl != "ACTIVE" || readiness.StrategyControl != "ACTIVE" || readiness.AccountControl != "ACTIVE" {
			return errors.New("entry controls are not active")
		}
		if !state.Flat || state.ActiveEpisodes != 0 || state.NAVMicros == 0 || state.DailyTurnoverMicros > maximumDailyTurnoverMicros-2*protocol.EntryNotionalMicros {
			return errors.New("entry account limits are not available")
		}
		if state.CollateralMicros == 0 || (state.MaintenanceMarginMicros > 0 && state.MaintenanceMarginMicros > state.CollateralMicros/2) {
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
	if !ok || settlement > protocol.EntryNotionalMicros || quotes.Spot.Side != "buy" || quotes.Perp.Side != "short" || quotes.Perp.ReduceOnly {
		return PairIntent{}, errors.New("entry spot quote exceeds the canary cap")
	}
	perpNotional, ok := derivedPerpNotional(quotes.Perp)
	if !ok || perpNotional > protocol.EntryNotionalMicros || settlement > 2*protocol.EntryNotionalMicros-perpNotional {
		return PairIntent{}, errors.New("entry perp quote exceeds the canary caps")
	}
	if quotes.Spot.ExpectedUIMultiplier != state.UIMultiplierE18 {
		return PairIntent{}, errors.New("quote UI multiplier does not match account state")
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
	createdAtMS := quotes.ObservedAtMS
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
		MinimumUnwindSettlementOut: uintString(settlement * minimumUnwindSettlementPpm / 1_000_000),
		ExpectedUIMultiplier:       quotes.Spot.ExpectedUIMultiplier,
		MinOracleRoundID:           quotes.Spot.MinOracleRoundID,
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
		PerpOrderExpiryMS:          createdAtMS + 300_000,
		EmergencyDeadlineMS:        createdAtMS + 600_000,
		ReconciliationDeadlineMS:   createdAtMS + 86_400_000,
		LeverageMicros:             1_000_000,
		CreatedAtMS:                createdAtMS,
		DeadlineMS:                 quotes.ExpiresAtMS,
		Evidence: FrozenEvidence{
			DatasetManifest:          input.Evaluation.DatasetManifest,
			StrategyVersion:          protocol.StrategyVersion,
			MarketManifest:           input.Evaluation.MarketManifest,
			QuoteBlockHash:           quotes.Spot.BlockHash,
			QuoteReceivedAtMS:        quotes.ObservedAtMS,
			QuoteExpiresAtMS:         quotes.ExpiresAtMS,
			UIMultiplierE18:          quotes.Spot.ExpectedUIMultiplier,
			PerpMarkPrice:            quotes.Perp.MarkPrice,
			EstimatedTotalCostMicros: input.Evaluation.EstimatedCostMicros,
		},
	}
	if intent.DeadlineMS <= nowMS || intent.DeadlineMS <= intent.CreatedAtMS {
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
	authority := quotes.ExitAuthority
	quoteMinimum, quoteMinimumOK := positiveDecimal(quotes.Spot.MinimumAmountOut)
	episodeMinimum, episodeMinimumOK := positiveDecimal(episode.MinimumSettlementAmountOut)
	if authority == nil || authority.IntentID != episode.PairIntentID ||
		!validHash(episode.PairIntentID) || domainHash(spotUnwindDomain, []byte(episode.PairIntentID)) != episode.SpotUnwindIntentID ||
		episode.SpotAmount != quotes.Spot.StockAmount || episode.PerpBaseAmount != quotes.Perp.BaseAmount ||
		!quoteMinimumOK || !episodeMinimumOK || quoteMinimum.Cmp(episodeMinimum) < 0 ||
		quotes.Spot.Side != "sell" || quotes.Perp.Side != "long" || !quotes.Perp.ReduceOnly ||
		quotes.Spot.ExpectedUIMultiplier != input.AccountState.UIMultiplierE18 {
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
		ExpectedUIMultiplier:       quotes.Spot.ExpectedUIMultiplier,
		MinOracleRoundID:           quotes.Spot.MinOracleRoundID,
		PerpBaseAmount:             quotes.Perp.BaseAmount,
		PerpLimitPrice:             quotes.Perp.LimitPrice,
		ReduceOnly:                 true,
		QuoteSourceSession:         authority.SourceSession,
		QuoteSourceEventID:         authority.SourceEventID,
		QuotePayloadSHA256:         authority.PayloadSHA256,
		ObservedAtMS:               quotes.ObservedAtMS,
		DeadlineMS:                 authority.SubmissionDeadlineMS,
		ReconciliationDeadlineMS:   authority.ReconciliationDeadlineMS,
	}
	encoded, err := json.Marshal(directive)
	if err != nil {
		return UnwindDirective{}, err
	}
	directive.ID = domainHash(unwindDirectiveDomain, encoded)
	return directive, nil
}

func exitReason(lifecycle string) (string, error) {
	switch lifecycle {
	case "running":
		return "strategy_exit", nil
	case "reducing", "closing":
		return "operator_exit", nil
	default:
		return "", errors.New("exit lifecycle has no coordinator reason")
	}
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
	adjusted.Div(adjusted, scale)
	if spotDecimals < perpDecimals {
		adjusted.Mul(adjusted, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(perpDecimals-spotDecimals)), nil))
	} else if spotDecimals > perpDecimals {
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(spotDecimals-perpDecimals)), nil)
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
	return value == "ACTIVE" || value == "REDUCE_ONLY"
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

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return value[14] >= '1' && value[14] <= '5' && strings.ContainsRune("89ab", rune(value[19]))
}

func domainHash(domain, payload []byte) string {
	digest := sha256.New()
	_, _ = digest.Write(domain)
	_, _ = digest.Write(payload)
	return "0x" + hex.EncodeToString(digest.Sum(nil))
}
