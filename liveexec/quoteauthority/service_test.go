package quoteauthority

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
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

func (f *fakeAdapter) Quote(_ context.Context, request AdapterRequest) (AdapterResult, error) {
	f.seen = request
	return f.result, f.err
}

func TestServicePinsEntryPolicyAndSignsExecutableQuotes(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	adapter := &fakeAdapter{result: adapterResult(protocol.ActionEntry)}
	service, err := NewService(adapter, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.UnixMilli(100_000) }
	request := protocol.QuoteRequest{
		RequestID:          testHash("request"),
		ExecutionAccountID: "account-canary-1",
		SourceEvaluationID: testHash("evaluation"),
		Action:             protocol.ActionEntry,
		RequestedAtMS:      99_500,
	}
	quote, err := service.Quote(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.seen.EntryNotional != protocol.EntryNotionalMicros || adapter.seen.ExecutionAccountID != request.ExecutionAccountID {
		t.Fatal("adapter did not receive fixed account-scoped request")
	}
	if quote.StrategyManifestSHA256 != protocol.StrategyManifestSHA256 || quote.Spot.StockToken != protocol.StockToken {
		t.Fatal("quote did not pin canonical strategy and route")
	}
	if err := quote.Verify(publicKey, 100_000); err != nil {
		t.Fatalf("authority produced unverifiable quote: %v", err)
	}
}

func TestServiceRejectsAdapterPolicyViolations(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	cases := map[string]func(*AdapterResult){
		"wrong token":     func(result *AdapterResult) { result.Spot.StockToken = "0x0000000000000000000000000000000000000001" },
		"wrong notional":  func(result *AdapterResult) { result.Spot.SettlementAmount = "24999999" },
		"wrong direction": func(result *AdapterResult) { result.Perp.Side = "long" },
		"stale":           func(result *AdapterResult) { result.ExpiresAtMS = 100_000 },
		"missing source":  func(result *AdapterResult) { result.Source.AdapterID = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			result := adapterResult(protocol.ActionEntry)
			mutate(&result)
			service, err := NewService(&fakeAdapter{result: result}, privateKey)
			if err != nil {
				t.Fatal(err)
			}
			service.now = func() time.Time { return time.UnixMilli(100_000) }
			_, err = service.Quote(context.Background(), protocol.QuoteRequest{
				RequestID: testHash("request"), ExecutionAccountID: "account-canary-1",
				SourceEvaluationID: testHash("evaluation"), Action: protocol.ActionEntry, RequestedAtMS: 99_500,
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
	service, _ := NewService(&fakeAdapter{err: want}, privateKey)
	service.now = func() time.Time { return time.UnixMilli(100_000) }
	_, err := service.Quote(context.Background(), protocol.QuoteRequest{
		RequestID: testHash("request"), ExecutionAccountID: "account-canary-1",
		SourceEvaluationID: testHash("evaluation"), Action: protocol.ActionEntry, RequestedAtMS: 99_500,
	})
	if !errors.Is(err, want) {
		t.Fatalf("adapter error was masked: %v", err)
	}
}

func adapterResult(action protocol.Action) AdapterResult {
	spotSide, perpSide, reduceOnly := "buy", "short", false
	if action == protocol.ActionUnwind {
		spotSide, perpSide, reduceOnly = "sell", "long", true
	}
	return AdapterResult{
		Source: protocol.SourceIdentity{AdapterID: "reviewed-adapter-v1", SpotSource: "spot-source", PerpSource: "perp-source", OracleRound: "round-1"},
		Spot: protocol.SpotQuote{
			Venue: protocol.SpotVenue, ChainID: protocol.ChainID, SettlementToken: protocol.SettlementToken,
			StockToken: protocol.StockToken, Router: protocol.Router, Side: spotSide,
			SettlementAmount: "25000000", StockAmount: "2000000", MinimumAmountOut: "1990000",
			ReferencePriceMicros: 25_000_000, BlockHash: testHash("block"), ObservedAtMS: 99_000,
		},
		Perp: protocol.PerpQuote{
			Venue: protocol.PerpVenue, Symbol: protocol.Symbol, MarketIndex: 101, Side: perpSide, ReduceOnly: reduceOnly,
			BaseAmount: 1_000_000, BaseDecimals: 6, PriceDecimals: 3, LimitPrice: 25_000, MarkPrice: 25_000, ObservedAtMS: 99_000,
		},
		ObservedAtMS: 99_000,
		ExpiresAtMS:  102_000,
	}
}

func testHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "0x" + hex.EncodeToString(digest[:])
}
