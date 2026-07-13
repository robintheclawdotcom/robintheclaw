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
	ExecutionAccountID string
	Action             protocol.Action
	EntryNotional      uint64
}

type AdapterResult struct {
	Source       protocol.SourceIdentity
	Spot         protocol.SpotQuote
	Perp         protocol.PerpQuote
	ObservedAtMS uint64
	ExpiresAtMS  uint64
}

type ExecutableQuoteAdapter interface {
	Quote(context.Context, AdapterRequest) (AdapterResult, error)
}

type Service struct {
	adapter    ExecutableQuoteAdapter
	privateKey ed25519.PrivateKey
	now        func() time.Time
}

func NewService(adapter ExecutableQuoteAdapter, privateKey ed25519.PrivateKey) (*Service, error) {
	if adapter == nil || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("quote adapter and signing key are required")
	}
	return &Service{adapter: adapter, privateKey: append(ed25519.PrivateKey(nil), privateKey...), now: time.Now}, nil
}

func (s *Service) Quote(ctx context.Context, request protocol.QuoteRequest) (protocol.QuoteBundle, error) {
	nowMS := uint64(s.now().UnixMilli())
	if !validHash(request.RequestID) || !validHash(request.SourceEvaluationID) || !validExecutionID(request.ExecutionAccountID) ||
		(request.Action != protocol.ActionEntry && request.Action != protocol.ActionUnwind) || request.RequestedAtMS > nowMS ||
		nowMS-request.RequestedAtMS > protocol.MaximumQuoteLifetimeMS {
		return protocol.QuoteBundle{}, errors.New("invalid quote request")
	}
	entryNotional := uint64(0)
	if request.Action == protocol.ActionEntry {
		entryNotional = protocol.EntryNotionalMicros
	}
	result, err := s.adapter.Quote(ctx, AdapterRequest{
		ExecutionAccountID: request.ExecutionAccountID,
		Action:             request.Action,
		EntryNotional:      entryNotional,
	})
	if err != nil {
		return protocol.QuoteBundle{}, err
	}
	if err := validateAdapterResult(request.Action, result, nowMS); err != nil {
		return protocol.QuoteBundle{}, err
	}
	bundle := protocol.QuoteBundle{
		SchemaVersion:          1,
		RequestID:              request.RequestID,
		ExecutionAccountID:     request.ExecutionAccountID,
		SourceEvaluationID:     request.SourceEvaluationID,
		StrategyVersion:        protocol.StrategyVersion,
		StrategyManifestSHA256: protocol.StrategyManifestSHA256,
		SourceConfigSHA256:     protocol.SourceConfigSHA256,
		RouteSHA256:            protocol.RouteSHA256,
		OraclePolicySHA256:     protocol.OraclePolicySHA256,
		RiskPolicySHA256:       protocol.RiskPolicySHA256,
		Action:                 request.Action,
		Source:                 result.Source,
		Spot:                   result.Spot,
		Perp:                   result.Perp,
		ObservedAtMS:           result.ObservedAtMS,
		ExpiresAtMS:            result.ExpiresAtMS,
	}
	if err := bundle.Sign(s.privateKey); err != nil {
		return protocol.QuoteBundle{}, err
	}
	if err := bundle.Verify(s.privateKey.Public().(ed25519.PublicKey), nowMS); err != nil {
		return protocol.QuoteBundle{}, err
	}
	return bundle, nil
}

func validateAdapterResult(action protocol.Action, result AdapterResult, nowMS uint64) error {
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
		perp.Side != perpSide || perp.ReduceOnly != reduceOnly || perp.ObservedAtMS != result.ObservedAtMS {
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
	minimum, ok := positiveDecimal(spot.MinimumAmountOut)
	if !ok || (action == protocol.ActionEntry && minimum.Cmp(stock) > 0) ||
		(action == protocol.ActionUnwind && minimum.Cmp(settlement) > 0) ||
		spot.ReferencePriceMicros == 0 || perp.BaseAmount == 0 || perp.LimitPrice == 0 || perp.MarkPrice == 0 {
		return errors.New("adapter returned invalid executable amounts")
	}
	if action == protocol.ActionEntry {
		if settlement.Cmp(new(big.Int).SetUint64(protocol.EntryNotionalMicros)) != 0 ||
			perpNotional(perp).Cmp(new(big.Int).SetUint64(protocol.EntryNotionalMicros)) != 0 {
			return errors.New("entry quote does not match the fixed notional")
		}
	}
	return nil
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
