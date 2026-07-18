package quoteauthority

import (
	"context"
	"crypto/ed25519"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

type AdapterRequest struct {
	RequestID                    string
	ExecutionAccountID           string
	IntentID                     string
	MarketManifest               string
	TargetStrategyManifestSHA256 string
	Action                       protocol.Action
	EntryNotional                uint64
}

type DurableSource struct {
	Session      string
	EventID      string
	Sequence     int64
	ReceivedAtMS uint64
}

type AdapterResult struct {
	Source        protocol.SourceIdentity
	Spot          protocol.SpotQuote
	Perp          protocol.PerpQuote
	DurableSource DurableSource
	ObservedAtMS  uint64
	ExpiresAtMS   uint64
}

type ExecutableQuoteAdapter interface {
	Quote(context.Context, AdapterRequest) (AdapterResult, error)
}

type MarketQuotePublisher interface {
	Publish(context.Context, protocol.MarketQuotePublication) (protocol.MarketQuoteReceipt, error)
}

type Service struct {
	adapter            ExecutableQuoteAdapter
	publisher          MarketQuotePublisher
	privateKey         ed25519.PrivateKey
	lighterMarketIndex uint32
	now                func() time.Time
}

func NewService(adapter ExecutableQuoteAdapter, publisher MarketQuotePublisher, privateKey ed25519.PrivateKey, lighterMarketIndex uint32) (*Service, error) {
	if adapter == nil || publisher == nil || len(privateKey) != ed25519.PrivateKeySize || lighterMarketIndex > 32767 {
		return nil, errors.New("quote adapter, market quote publisher, signing key, and reviewed market index are required")
	}
	return &Service{
		adapter: adapter, publisher: publisher, privateKey: append(ed25519.PrivateKey(nil), privateKey...),
		lighterMarketIndex: lighterMarketIndex, now: time.Now,
	}, nil
}

func (s *Service) Quote(ctx context.Context, request protocol.QuoteRequest) (protocol.QuoteBundle, error) {
	nowMS := uint64(s.now().UnixMilli())
	if !validHash(request.RequestID) || !validHash(request.SourceEvaluationID) || !validHash(request.MarketManifest) ||
		!validExecutionID(request.ExecutionAccountID) ||
		(request.Action != protocol.ActionEntry && request.Action != protocol.ActionUnwind) || request.RequestedAtMS > nowMS ||
		nowMS-request.RequestedAtMS > protocol.MaximumQuoteLifetimeMS {
		return protocol.QuoteBundle{}, errors.New("invalid quote request")
	}
	if (request.Action == protocol.ActionEntry &&
		(request.IntentID != "" || request.TargetStrategyManifestSHA256 != "")) ||
		(request.Action == protocol.ActionUnwind &&
			(!validHash(request.IntentID) ||
				!protocol.IsAllowedUnwindTargetStrategyManifest(request.TargetStrategyManifestSHA256))) {
		return protocol.QuoteBundle{}, errors.New("invalid quote intent binding")
	}
	entryNotional := uint64(0)
	if request.Action == protocol.ActionEntry {
		entryNotional = protocol.EntryNotionalMicros
	}
	result, err := s.adapter.Quote(ctx, AdapterRequest{
		RequestID:                    request.RequestID,
		ExecutionAccountID:           request.ExecutionAccountID,
		IntentID:                     request.IntentID,
		MarketManifest:               request.MarketManifest,
		TargetStrategyManifestSHA256: request.TargetStrategyManifestSHA256,
		Action:                       request.Action,
		EntryNotional:                entryNotional,
	})
	if err != nil {
		return protocol.QuoteBundle{}, err
	}
	if err := validateAdapterResult(request.Action, result, s.lighterMarketIndex, nowMS); err != nil {
		return protocol.QuoteBundle{}, err
	}
	bundle := protocol.QuoteBundle{
		SchemaVersion:                protocol.QuoteSchemaVersion,
		RequestID:                    request.RequestID,
		ExecutionAccountID:           request.ExecutionAccountID,
		SourceEvaluationID:           request.SourceEvaluationID,
		MarketManifest:               request.MarketManifest,
		StrategyVersion:              protocol.StrategyVersion,
		StrategyManifestSHA256:       protocol.StrategyManifestSHA256,
		TargetStrategyManifestSHA256: request.TargetStrategyManifestSHA256,
		SourceConfigSHA256:           protocol.SourceConfigSHA256,
		RouteSHA256:                  protocol.RouteSHA256,
		OraclePolicySHA256:           protocol.OraclePolicySHA256,
		RiskPolicySHA256:             protocol.RiskPolicySHA256,
		Action:                       request.Action,
		Source:                       result.Source,
		Spot:                         result.Spot,
		Perp:                         result.Perp,
		ObservedAtMS:                 result.ObservedAtMS,
		ExpiresAtMS:                  result.ExpiresAtMS,
	}
	reconciliationDeadline := uint64(0)
	if request.Action == protocol.ActionUnwind {
		var ok bool
		reconciliationDeadline, ok = addUint64(result.ExpiresAtMS, protocol.MaximumExitReconciliationMS)
		if !ok {
			return protocol.QuoteBundle{}, errors.New("exit quote deadlines overflow")
		}
	}
	publication, ok := marketPublication(request, result, reconciliationDeadline)
	if !ok {
		return protocol.QuoteBundle{}, errors.New("executable quote cannot be persisted")
	}
	receipt, err := s.publisher.Publish(ctx, publication)
	if err != nil {
		return protocol.QuoteBundle{}, err
	}
	if (receipt.Status != "recorded" && receipt.Status != "duplicate") ||
		receipt.SourceSession != result.DurableSource.Session || receipt.SourceEventID != result.DurableSource.EventID ||
		!validDigest(receipt.PayloadSHA256) {
		return protocol.QuoteBundle{}, errors.New("coordinator did not prove quote persistence")
	}
	if request.Action == protocol.ActionUnwind {
		bundle.ExitAuthority = &protocol.ExitQuoteAuthority{
			Source:                   "execution-authority",
			SourceSession:            receipt.SourceSession,
			SourceEventID:            receipt.SourceEventID,
			SourceSequence:           result.DurableSource.Sequence,
			ExecutionAccountID:       request.ExecutionAccountID,
			IntentID:                 request.IntentID,
			MarketManifest:           request.MarketManifest,
			PayloadSHA256:            receipt.PayloadSHA256,
			ReceivedAtMS:             result.DurableSource.ReceivedAtMS,
			SubmissionDeadlineMS:     result.ExpiresAtMS,
			ReconciliationDeadlineMS: reconciliationDeadline,
		}
	}
	if err := bundle.Sign(s.privateKey); err != nil {
		return protocol.QuoteBundle{}, err
	}
	if err := bundle.Verify(s.privateKey.Public().(ed25519.PublicKey), s.lighterMarketIndex, nowMS); err != nil {
		return protocol.QuoteBundle{}, err
	}
	return bundle, nil
}

func validateAdapterResult(action protocol.Action, result AdapterResult, lighterMarketIndex uint32, nowMS uint64) error {
	if result.ObservedAtMS > nowMS || result.ExpiresAtMS <= nowMS || result.ExpiresAtMS <= result.ObservedAtMS ||
		result.ExpiresAtMS-result.ObservedAtMS > protocol.MaximumQuoteLifetimeMS {
		return errors.New("adapter returned stale quote")
	}
	if result.Source.AdapterID == "" || result.Source.SpotSource == "" || result.Source.PerpSource == "" || result.Source.OracleRound == "" {
		return errors.New("adapter source identity is incomplete")
	}
	spotSide, perpSide, reduceOnly := "buy", "short", false
	if action == protocol.ActionUnwind {
		spotSide, perpSide, reduceOnly = "sell", "long", true
	}
	spot := result.Spot
	perp := result.Perp
	if spot.Venue != protocol.SpotVenue || spot.ChainID != protocol.ChainID || spot.SettlementToken != protocol.SettlementToken ||
		spot.StockToken != protocol.StockToken || spot.Router != protocol.Router || spot.Side != spotSide || spot.BlockHash == "" ||
		spot.ObservedAtMS != result.ObservedAtMS || perp.Venue != protocol.PerpVenue || perp.Symbol != protocol.Symbol ||
		perp.Side != perpSide || perp.ReduceOnly != reduceOnly || perp.MarketIndex != lighterMarketIndex ||
		perp.ObservedAtMS != result.ObservedAtMS {
		return errors.New("adapter returned a route or direction mismatch")
	}
	settlement, ok := positiveDecimal(spot.SettlementAmount)
	if !ok {
		return errors.New("adapter returned invalid settlement amount")
	}
	stock, ok := positiveDecimal(spot.StockAmount)
	if !ok {
		return errors.New("adapter returned invalid stock amount")
	}
	multiplier, multiplierOK := positiveDecimal(spot.ExpectedUIMultiplier)
	minimumRound, minimumRoundOK := positiveDecimal(spot.MinOracleRoundID)
	if !multiplierOK || !minimumRoundOK || multiplier.BitLen() > 256 || minimumRound.BitLen() > 80 ||
		result.Source.OracleRound != spot.MinOracleRoundID {
		return errors.New("adapter returned invalid execution policy")
	}
	if action == protocol.ActionEntry && perp.Phase != "" {
		return errors.New("entry quote contains an unwind phase")
	}
	if action == protocol.ActionUnwind && perp.Phase != "perp_and_spot" && perp.Phase != "spot_only" {
		return errors.New("unwind quote phase is invalid")
	}
	spotOnly := action == protocol.ActionUnwind && perp.Phase == "spot_only"
	minimum, ok := positiveDecimal(spot.MinimumAmountOut)
	if !ok || (action == protocol.ActionEntry && minimum.Cmp(stock) > 0) ||
		(action == protocol.ActionUnwind && minimum.Cmp(settlement) > 0) ||
		spot.ReferencePriceMicros == 0 || (!spotOnly && perp.BaseAmount == 0) ||
		(spotOnly && perp.BaseAmount != 0) || perp.LimitPrice == 0 || perp.MarkPrice == 0 {
		return errors.New("adapter returned invalid executable amounts")
	}
	if action == protocol.ActionEntry {
		derivedPerpNotional := perpNotional(perp)
		cap := new(big.Int).SetUint64(protocol.EntryNotionalMicros)
		gross := new(big.Int).Add(new(big.Int).Set(settlement), derivedPerpNotional)
		if settlement.Cmp(cap) > 0 || derivedPerpNotional.Cmp(cap) > 0 || gross.Cmp(new(big.Int).Mul(cap, big.NewInt(2))) > 0 {
			return errors.New("entry quote exceeds the canary notional caps")
		}
	}
	if !validSourcePart(result.DurableSource.Session, 128) ||
		!validSourcePart(result.DurableSource.EventID, 256) || result.DurableSource.Sequence < 0 ||
		result.DurableSource.ReceivedAtMS < result.ObservedAtMS || result.DurableSource.ReceivedAtMS >= result.ExpiresAtMS ||
		result.DurableSource.ReceivedAtMS > nowMS {
		return errors.New("adapter returned invalid durable quote identity")
	}
	return nil
}

func marketPublication(request protocol.QuoteRequest, result AdapterResult, reconciliationDeadline uint64) (protocol.MarketQuotePublication, bool) {
	publisherAt, publisherOK := toInt64(result.ObservedAtMS)
	receivedAt, receivedOK := toInt64(result.DurableSource.ReceivedAtMS)
	expiresAt, expiresOK := toInt64(result.ExpiresAtMS)
	reconciliation, reconciliationOK := toInt64(reconciliationDeadline)
	if !publisherOK || !receivedOK || !expiresOK || (request.Action == protocol.ActionUnwind && !reconciliationOK) {
		return protocol.MarketQuotePublication{}, false
	}
	perpUnwindBaseAmount := result.Perp.BaseAmount
	publication := protocol.MarketQuotePublication{
		Source:                       "lighter-auth",
		SourceSession:                result.DurableSource.Session,
		SourceEventID:                result.DurableSource.EventID,
		SourceSequence:               result.DurableSource.Sequence,
		ExecutionAccountID:           request.ExecutionAccountID,
		MarketManifest:               request.MarketManifest,
		StrategyManifestSHA256:       protocol.StrategyManifestSHA256,
		TargetStrategyManifestSHA256: request.TargetStrategyManifestSHA256,
		RouteSHA256:                  protocol.RouteSHA256,
		LighterMarketIndex:           result.Perp.MarketIndex,
		QuoteBlockHash:               result.Spot.BlockHash,
		MarkPrice:                    result.Perp.MarkPrice,
		ExpectedUIMultiplier:         result.Spot.ExpectedUIMultiplier,
		MinOracleRoundID:             result.Spot.MinOracleRoundID,
		PublisherAtMS:                publisherAt,
		ReceivedAtMS:                 receivedAt,
		ExpiresAtMS:                  expiresAt,
		IntentID:                     request.IntentID,
		SpotUnwindAmountIn:           result.Spot.StockAmount,
		SpotUnwindExpectedAmountOut:  result.Spot.SettlementAmount,
		UnwindPhase:                  result.Perp.Phase,
		PerpUnwindBaseAmount:         &perpUnwindBaseAmount,
		PerpUnwindLimitPrice:         result.Perp.LimitPrice,
		SubmissionDeadlineMS:         expiresAt,
		ReconciliationDeadlineMS:     reconciliation,
	}
	if request.Action == protocol.ActionEntry {
		publication.ExecutionAccountID = ""
		publication.StrategyManifestSHA256 = ""
		publication.TargetStrategyManifestSHA256 = ""
		publication.RouteSHA256 = ""
		publication.LighterMarketIndex = 0
		publication.IntentID = ""
		publication.SpotUnwindAmountIn = ""
		publication.SpotUnwindExpectedAmountOut = ""
		publication.UnwindPhase = ""
		publication.PerpUnwindBaseAmount = nil
		publication.PerpUnwindLimitPrice = 0
		publication.SubmissionDeadlineMS = 0
		publication.ReconciliationDeadlineMS = 0
	} else {
		publication.Source = "execution-authority"
	}
	return publication, true
}

func addUint64(left, right uint64) (uint64, bool) {
	value := left + right
	return value, value >= left
}

func toInt64(value uint64) (int64, bool) {
	converted := int64(value)
	return converted, converted >= 0 && uint64(converted) == value
}

func perpNotional(quote protocol.PerpQuote) *big.Int {
	price := quote.LimitPrice
	if quote.MarkPrice > price {
		price = quote.MarkPrice
	}
	numerator := new(big.Int).SetUint64(quote.BaseAmount)
	numerator.Mul(numerator, new(big.Int).SetUint64(uint64(price)))
	numerator.Mul(numerator, big.NewInt(1_000_000))
	denominator := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(quote.BaseDecimals)+int64(quote.PriceDecimals)), nil)
	numerator.Add(numerator, new(big.Int).Sub(denominator, big.NewInt(1)))
	return numerator.Div(numerator, denominator)
}

func positiveDecimal(value string) (*big.Int, bool) {
	if value == "" || value[0] == '+' || (len(value) > 1 && value[0] == '0') {
		return nil, false
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	return parsed, ok && parsed.Sign() > 0
}

func validHash(value string) bool {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") || value == "0x"+strings.Repeat("0", 64) {
		return false
	}
	for _, char := range value[2:] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
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
