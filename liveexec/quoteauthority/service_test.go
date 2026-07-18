package quoteauthority

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

type fakeAdapter struct {
	result AdapterResult
	err    error
	seen   AdapterRequest
}

type fakePublisher struct {
	quotes []protocol.MarketQuotePublication
	err    error
}

func (f *fakePublisher) Publish(_ context.Context, quote protocol.MarketQuotePublication) (protocol.MarketQuoteReceipt, error) {
	f.quotes = append(f.quotes, quote)
	if f.err != nil {
		return protocol.MarketQuoteReceipt{}, f.err
	}
	encoded, _ := json.Marshal(quote)
	digest := sha256.Sum256(encoded)
	return protocol.MarketQuoteReceipt{
		Status: "recorded", SourceSession: quote.SourceSession, SourceEventID: quote.SourceEventID,
		PayloadSHA256: hex.EncodeToString(digest[:]),
	}, nil
}

func (f *fakeAdapter) Quote(_ context.Context, request AdapterRequest) (AdapterResult, error) {
	f.seen = request
	return f.result, f.err
}

func TestServicePinsEntryPolicyAndSignsExecutableQuotes(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	adapter := &fakeAdapter{result: adapterResult(protocol.ActionEntry)}
	publisher := &fakePublisher{}
	service, err := NewService(adapter, publisher, privateKey, 101)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.UnixMilli(100_000) }
	request := protocol.QuoteRequest{
		RequestID:          testHash("request"),
		ExecutionAccountID: "account-canary-1",
		SourceEvaluationID: testHash("evaluation"),
		MarketManifest:     testHash("market"),
		Action:             protocol.ActionEntry,
		RequestedAtMS:      99_500,
	}
	quote, err := service.Quote(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.seen.EntryNotional != protocol.EntryNotionalMicros || adapter.seen.ExecutionAccountID != request.ExecutionAccountID ||
		adapter.seen.RequestID != request.RequestID || adapter.seen.MarketManifest != request.MarketManifest {
		t.Fatal("adapter did not receive fixed account-scoped request")
	}
	if quote.StrategyManifestSHA256 != protocol.StrategyManifestSHA256 ||
		quote.TargetStrategyManifestSHA256 != "" || quote.Spot.StockToken != protocol.StockToken {
		t.Fatal("quote did not pin canonical strategy and route")
	}
	if len(publisher.quotes) != 1 || publisher.quotes[0].Source != "lighter-auth" ||
		publisher.quotes[0].ExpectedUIMultiplier != quote.Spot.ExpectedUIMultiplier || publisher.quotes[0].MinOracleRoundID != quote.Spot.MinOracleRoundID {
		t.Fatal("entry quote was signed without durable market evidence")
	}
	encoded, err := json.Marshal(publisher.quotes[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"target_strategy_manifest_sha256",
		"unwind_phase",
		"perp_unwind_base_amount",
		"perp_unwind_limit_price",
	} {
		if _, exists := fields[field]; exists {
			t.Fatalf("entry publication included exit-only field %q", field)
		}
	}
	if err := quote.Verify(publicKey, 101, 100_000); err != nil {
		t.Fatalf("authority produced unverifiable quote: %v", err)
	}
}

func TestServicePersistsExitAuthorityBeforeSigning(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	result := adapterResult(protocol.ActionUnwind)
	result.Perp.BaseAmount = 250_000
	adapter := &fakeAdapter{result: result}
	publisher := &fakePublisher{}
	service, err := NewService(adapter, publisher, privateKey, 101)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.UnixMilli(100_000) }
	request := protocol.QuoteRequest{
		RequestID: testHash("exit-request"), ExecutionAccountID: "account-canary-1",
		SourceEvaluationID: testHash("exit-evaluation"), MarketManifest: testHash("market"),
		IntentID:                     testHash("open-intent"),
		TargetStrategyManifestSHA256: protocol.PreviousStrategyManifestSHA256,
		Action:                       protocol.ActionUnwind, RequestedAtMS: 99_500,
	}
	quote, err := service.Quote(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(publisher.quotes) != 1 || adapter.seen.IntentID != request.IntentID ||
		adapter.seen.TargetStrategyManifestSHA256 != request.TargetStrategyManifestSHA256 {
		t.Fatal("exit quote was signed without exact coordinator publication")
	}
	persisted := publisher.quotes[0]
	if persisted.ExecutionAccountID != request.ExecutionAccountID || persisted.IntentID != request.IntentID ||
		persisted.StrategyManifestSHA256 != protocol.StrategyManifestSHA256 ||
		persisted.TargetStrategyManifestSHA256 != request.TargetStrategyManifestSHA256 ||
		persisted.RouteSHA256 != protocol.RouteSHA256 || persisted.MarketManifest != request.MarketManifest || persisted.LighterMarketIndex != 101 ||
		persisted.SpotUnwindAmountIn != result.Spot.StockAmount || persisted.SpotUnwindExpectedAmountOut != result.Spot.SettlementAmount ||
		persisted.UnwindPhase != result.Perp.Phase || persisted.PerpUnwindBaseAmount == nil ||
		*persisted.PerpUnwindBaseAmount != result.Perp.BaseAmount ||
		persisted.PerpUnwindLimitPrice != result.Perp.LimitPrice ||
		persisted.ExpectedUIMultiplier != result.Spot.ExpectedUIMultiplier ||
		persisted.MinOracleRoundID != result.Spot.MinOracleRoundID ||
		persisted.SubmissionDeadlineMS != int64(result.ExpiresAtMS) || quote.ExitAuthority == nil ||
		quote.ExitAuthority.PayloadSHA256 == "" || quote.Perp.BaseAmount != 250_000 ||
		quote.StrategyManifestSHA256 != protocol.StrategyManifestSHA256 ||
		quote.TargetStrategyManifestSHA256 != request.TargetStrategyManifestSHA256 {
		t.Fatal("persisted exit authority omitted a bound field")
	}
	if err := quote.Verify(publicKey, 101, 100_000); err != nil {
		t.Fatalf("persisted exit quote was not verifiable: %v", err)
	}
}

func TestServiceNeverSignsUnpersistedExit(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	publisher := &fakePublisher{err: ErrMarketQuoteAmbiguous}
	service, _ := NewService(&fakeAdapter{result: adapterResult(protocol.ActionUnwind)}, publisher, privateKey, 101)
	service.now = func() time.Time { return time.UnixMilli(100_000) }
	_, err := service.Quote(context.Background(), protocol.QuoteRequest{
		RequestID: testHash("exit-request"), ExecutionAccountID: "account-canary-1",
		SourceEvaluationID: testHash("exit-evaluation"), MarketManifest: testHash("market"),
		IntentID: testHash("open-intent"), TargetStrategyManifestSHA256: protocol.StrategyManifestSHA256,
		Action: protocol.ActionUnwind, RequestedAtMS: 99_500,
	})
	if !errors.Is(err, ErrMarketQuoteAmbiguous) {
		t.Fatalf("unpersisted exit quote was not rejected: %v", err)
	}
}

func TestServiceSerializesSpotOnlyZeroPerpBase(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	result := adapterResult(protocol.ActionUnwind)
	result.Perp.Phase = "spot_only"
	result.Perp.BaseAmount = 0
	publisher := &fakePublisher{}
	service, _ := NewService(&fakeAdapter{result: result}, publisher, privateKey, 101)
	service.now = func() time.Time { return time.UnixMilli(100_000) }
	_, err := service.Quote(context.Background(), protocol.QuoteRequest{
		RequestID: testHash("spot-only-request"), ExecutionAccountID: "account-canary-1",
		SourceEvaluationID: testHash("spot-only-evaluation"), MarketManifest: testHash("market"),
		IntentID: testHash("open-intent"), TargetStrategyManifestSHA256: protocol.StrategyManifestSHA256,
		Action: protocol.ActionUnwind, RequestedAtMS: 99_500,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(publisher.quotes[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["perp_unwind_base_amount"]) != "0" {
		t.Fatalf("spot-only publication omitted explicit zero perp base: %s", encoded)
	}
}

func TestServiceRejectsSpotOnlyNonzeroPerpBase(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	result := adapterResult(protocol.ActionUnwind)
	result.Perp.Phase = "spot_only"
	result.Perp.BaseAmount = 1
	service, _ := NewService(&fakeAdapter{result: result}, &fakePublisher{}, privateKey, 101)
	service.now = func() time.Time { return time.UnixMilli(100_000) }
	_, err := service.Quote(context.Background(), protocol.QuoteRequest{
		RequestID: testHash("invalid-spot-only-request"), ExecutionAccountID: "account-canary-1",
		SourceEvaluationID: testHash("invalid-spot-only-evaluation"), MarketManifest: testHash("market"),
		IntentID: testHash("open-intent"), TargetStrategyManifestSHA256: protocol.StrategyManifestSHA256,
		Action: protocol.ActionUnwind, RequestedAtMS: 99_500,
	})
	if err == nil {
		t.Fatal("spot-only adapter result accepted a nonzero perp base")
	}
}

func TestServiceRejectsInvalidTargetManifestBindings(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	service, _ := NewService(
		&fakeAdapter{result: adapterResult(protocol.ActionUnwind)},
		&fakePublisher{},
		privateKey,
		101,
	)
	service.now = func() time.Time { return time.UnixMilli(100_000) }
	base := protocol.QuoteRequest{
		RequestID: testHash("target-request"), ExecutionAccountID: "account-canary-1",
		SourceEvaluationID: testHash("target-evaluation"), MarketManifest: testHash("market"),
		IntentID: testHash("open-intent"), Action: protocol.ActionUnwind, RequestedAtMS: 99_500,
	}
	for _, target := range []string{"", testHash("unknown-target")[2:]} {
		request := base
		request.TargetStrategyManifestSHA256 = target
		if _, err := service.Quote(context.Background(), request); err == nil {
			t.Fatalf("invalid unwind target %q was accepted", target)
		}
	}

	entry := base
	entry.IntentID = ""
	entry.TargetStrategyManifestSHA256 = protocol.PreviousStrategyManifestSHA256
	entry.Action = protocol.ActionEntry
	if _, err := service.Quote(context.Background(), entry); err == nil {
		t.Fatal("entry request accepted an unwind target")
	}
}

func TestServiceRejectsAdapterPolicyViolations(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	cases := map[string]func(*AdapterResult){
		"wrong token":       func(result *AdapterResult) { result.Spot.StockToken = "0x0000000000000000000000000000000000000001" },
		"over cap":          func(result *AdapterResult) { result.Spot.SettlementAmount = "25000001" },
		"wrong direction":   func(result *AdapterResult) { result.Perp.Side = "long" },
		"stale":             func(result *AdapterResult) { result.ExpiresAtMS = 100_000 },
		"missing source":    func(result *AdapterResult) { result.Source.AdapterID = "" },
		"unreviewed market": func(result *AdapterResult) { result.Perp.MarketIndex = 102 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			result := adapterResult(protocol.ActionEntry)
			mutate(&result)
			service, err := NewService(&fakeAdapter{result: result}, &fakePublisher{}, privateKey, 101)
			if err != nil {
				t.Fatal(err)
			}
			service.now = func() time.Time { return time.UnixMilli(100_000) }
			_, err = service.Quote(context.Background(), protocol.QuoteRequest{
				RequestID: testHash("request"), ExecutionAccountID: "account-canary-1",
				SourceEvaluationID: testHash("evaluation"), MarketManifest: testHash("market"), Action: protocol.ActionEntry, RequestedAtMS: 99_500,
			})
			if err == nil {
				t.Fatal("invalid adapter result accepted")
			}
		})
	}
}

func TestServiceDoesNotMaskAdapterFailure(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	want := errors.New("both venue quote sources unavailable")
	service, _ := NewService(&fakeAdapter{err: want}, &fakePublisher{}, privateKey, 101)
	service.now = func() time.Time { return time.UnixMilli(100_000) }
	_, err := service.Quote(context.Background(), protocol.QuoteRequest{
		RequestID: testHash("request"), ExecutionAccountID: "account-canary-1",
		SourceEvaluationID: testHash("evaluation"), MarketManifest: testHash("market"), Action: protocol.ActionEntry, RequestedAtMS: 99_500,
	})
	if !errors.Is(err, want) {
		t.Fatalf("adapter error was masked: %v", err)
	}
}

func adapterResult(action protocol.Action) AdapterResult {
	spotSide, perpSide, phase, reduceOnly := "buy", "short", "", false
	if action == protocol.ActionUnwind {
		spotSide, perpSide, phase, reduceOnly = "sell", "long", "perp_and_spot", true
	}
	return AdapterResult{
		Source: protocol.SourceIdentity{AdapterID: "reviewed-adapter-v1", SpotSource: "spot-source", PerpSource: "perp-source", OracleRound: "101"},
		Spot: protocol.SpotQuote{
			Venue: protocol.SpotVenue, ChainID: protocol.ChainID, SettlementToken: protocol.SettlementToken,
			StockToken: protocol.StockToken, Router: protocol.Router, Side: spotSide,
			SettlementAmount: "25000000", StockAmount: "2000000", MinimumAmountOut: "1990000",
			ReferencePriceMicros: 25_000_000, BlockHash: testHash("block"), ObservedAtMS: 99_000,
			ExpectedUIMultiplier: "500000000000000000", MinOracleRoundID: "101",
		},
		Perp: protocol.PerpQuote{
			Venue: protocol.PerpVenue, Symbol: protocol.Symbol, MarketIndex: 101, Side: perpSide, ReduceOnly: reduceOnly, Phase: phase,
			BaseAmount: 1_000_000, BaseDecimals: 6, PriceDecimals: 3, LimitPrice: 25_000, MarkPrice: 25_000, ObservedAtMS: 99_000,
		},
		DurableSource: DurableSource{Session: "authority-session-1", EventID: "authority-event-1", Sequence: 1, ReceivedAtMS: 99_500},
		ObservedAtMS:  99_000,
		ExpiresAtMS:   102_000,
	}
}

func testHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "0x" + hex.EncodeToString(digest[:])
}
