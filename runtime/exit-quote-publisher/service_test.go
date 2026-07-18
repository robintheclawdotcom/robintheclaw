package exitquote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

const testMarket = uint32(17)

type memoryStore struct {
	candidates []Candidate
	persisted  map[string]bool
	checks     []Candidate
	evidence   []PersistenceEvidence
}

func (store *memoryStore) Candidates(_ context.Context, _ time.Time, _ int) ([]Candidate, error) {
	var pending []Candidate
	for _, candidate := range store.candidates {
		if !store.persisted[candidate.ActionID] {
			pending = append(pending, candidate)
		}
	}
	return pending, nil
}

func (store *memoryStore) Persisted(_ context.Context, candidate Candidate, evidence PersistenceEvidence, _ time.Time) (bool, error) {
	store.checks = append(store.checks, candidate)
	store.evidence = append(store.evidence, evidence)
	return store.persisted[candidate.ActionID] && evidence.PayloadSHA256 == digest64('a'), nil
}

type quoteStub struct {
	private        ed25519.PrivateKey
	candidates     map[string]Candidate
	store          *memoryStore
	requests       []protocol.QuoteRequest
	errors         []error
	persistOnError bool
	mutate         func(*protocol.QuoteBundle)
}

func (stub *quoteStub) Quote(_ context.Context, request protocol.QuoteRequest) (protocol.QuoteBundle, error) {
	stub.requests = append(stub.requests, request)
	candidate := stub.candidates[request.ExecutionAccountID]
	if len(stub.errors) > 0 {
		err := stub.errors[0]
		stub.errors = stub.errors[1:]
		if err != nil {
			if stub.persistOnError {
				stub.store.persisted[candidate.ActionID] = true
			}
			return protocol.QuoteBundle{}, err
		}
	}
	quote := validQuote(timelessNow, stub.private, request, candidate)
	if stub.mutate != nil {
		stub.mutate(&quote)
		if err := quote.Sign(stub.private); err != nil {
			return protocol.QuoteBundle{}, err
		}
	}
	stub.store.persisted[candidate.ActionID] = true
	return quote, nil
}

var timelessNow = time.UnixMilli(1_800_000_000_000)

func TestPublisherRequestsIndependentAccountScopedExits(t *testing.T) {
	private, public := keypair(t)
	first := candidate("account-one", "1", PhasePerpAndSpot, 75)
	second := candidate("account-two", "2", PhaseSpotOnly, 0)
	store := &memoryStore{candidates: []Candidate{first, second}, persisted: map[string]bool{}}
	quotes := &quoteStub{private: private, candidates: map[string]Candidate{
		first.ExecutionAccountID: first, second.ExecutionAccountID: second,
	}, store: store}
	publisher := mustPublisher(t, store, quotes, public)
	if err := publisher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(quotes.requests) != 2 || len(store.checks) != 2 ||
		quotes.requests[0].ExecutionAccountID == quotes.requests[1].ExecutionAccountID {
		t.Fatal("two accounts were not quoted independently")
	}
	if quotes.requests[0].Action != protocol.ActionUnwind || quotes.requests[1].Action != protocol.ActionUnwind {
		t.Fatal("publisher requested a non-unwind action")
	}
	for _, evidence := range store.evidence {
		if evidence.ExpectedUIMultiplier != "500000000000000000" || evidence.MinOracleRoundID != "101" {
			t.Fatal("exit persistence did not bind the spot oracle policy")
		}
	}
}

func TestPublisherRetriesExactRequestAfterAmbiguousFailure(t *testing.T) {
	private, public := keypair(t)
	candidate := candidate("account-retry", "3", PhasePerpAndSpot, 40)
	store := &memoryStore{candidates: []Candidate{candidate}, persisted: map[string]bool{}}
	quotes := &quoteStub{private: private, candidates: map[string]Candidate{candidate.ExecutionAccountID: candidate},
		store: store, errors: []error{errors.New("ambiguous response")}}
	publisher := mustPublisher(t, store, quotes, public)
	if err := publisher.RunOnce(context.Background()); err == nil {
		t.Fatal("ambiguous quote request was treated as persisted")
	}
	if err := publisher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(quotes.requests) != 2 || !reflect.DeepEqual(quotes.requests[0], quotes.requests[1]) {
		t.Fatal("crash retry changed the deterministic quote request")
	}
}

func TestPublisherDoesNotRetryAfterAmbiguousPersistedRequest(t *testing.T) {
	private, public := keypair(t)
	candidate := candidate("account-persisted", "4", PhasePerpAndSpot, 55)
	store := &memoryStore{candidates: []Candidate{candidate}, persisted: map[string]bool{}}
	quotes := &quoteStub{private: private, candidates: map[string]Candidate{candidate.ExecutionAccountID: candidate}, store: store}
	quotes.errors = []error{errors.New("response lost after persistence")}
	quotes.persistOnError = true
	publisher := mustPublisher(t, store, quotes, public)
	if err := publisher.RunOnce(context.Background()); err == nil {
		t.Fatal("lost response was not reported")
	}
	if err := publisher.RunOnce(context.Background()); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("persisted quote was requested again: %v", err)
	}
}

func TestPublisherRejectsCrossAccountAndQuantitySubstitution(t *testing.T) {
	private, public := keypair(t)
	tests := []struct {
		name   string
		mutate func(*protocol.QuoteBundle)
	}{
		{name: "account", mutate: func(quote *protocol.QuoteBundle) {
			quote.ExecutionAccountID = "account-other"
		}},
		{name: "remaining", mutate: func(quote *protocol.QuoteBundle) {
			quote.Perp.BaseAmount++
		}},
		{name: "episode", mutate: func(quote *protocol.QuoteBundle) {
			quote.ExitAuthority.IntentID = hash("other")
		}},
		{name: "target_manifest", mutate: func(quote *protocol.QuoteBundle) {
			quote.TargetStrategyManifestSHA256 = digest64('c')
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := candidate("account-mismatch", "5", PhasePerpAndSpot, 25)
			store := &memoryStore{candidates: []Candidate{candidate}, persisted: map[string]bool{}}
			quotes := &quoteStub{private: private, candidates: map[string]Candidate{candidate.ExecutionAccountID: candidate},
				store: store, mutate: test.mutate}
			publisher := mustPublisher(t, store, quotes, public)
			if err := publisher.RunOnce(context.Background()); err == nil || len(store.checks) != 0 {
				t.Fatal("substituted quote reached persistence confirmation")
			}
		})
	}
}

func TestPublisherHandlesPartialAndSpotOnlyPhases(t *testing.T) {
	partial := candidate("account-partial", "6", PhasePerpAndSpot, 7)
	spotOnly := candidate("account-spot", "7", PhaseSpotOnly, 0)
	for _, candidate := range []Candidate{partial, spotOnly} {
		request, err := quoteRequest(candidate, uint64(timelessNow.UnixMilli()))
		if err != nil || request.IntentID != candidate.IntentID || request.Action != protocol.ActionUnwind {
			t.Fatalf("invalid phase request: %+v %v", request, err)
		}
	}
	invalid := partial
	invalid.PerpBaseAmount = 0
	if _, err := quoteRequest(invalid, uint64(timelessNow.UnixMilli())); err == nil {
		t.Fatal("zero remaining perp was accepted for a combined unwind")
	}
}

func TestQuoteRequestBindsTargetStrategyManifest(t *testing.T) {
	first := candidate("account-target", "8", PhasePerpAndSpot, 7)
	second := first
	second.TargetStrategyManifestSHA256 = digest64('c')
	firstRequest, err := quoteRequest(first, uint64(timelessNow.UnixMilli()))
	if err != nil {
		t.Fatal(err)
	}
	secondRequest, err := quoteRequest(second, uint64(timelessNow.UnixMilli()))
	if err != nil {
		t.Fatal(err)
	}
	if firstRequest.TargetStrategyManifestSHA256 != first.TargetStrategyManifestSHA256 ||
		firstRequest.RequestID == secondRequest.RequestID ||
		firstRequest.SourceEvaluationID == secondRequest.SourceEvaluationID {
		t.Fatal("target strategy manifest was not domain-bound")
	}
}

func TestPublisherHasNoWorkWithoutPendingExit(t *testing.T) {
	_, public := keypair(t)
	store := &memoryStore{persisted: map[string]bool{}}
	publisher := mustPublisher(t, store, &quoteStub{}, public)
	if err := publisher.RunOnce(context.Background()); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("unexpected empty result: %v", err)
	}
}

func candidate(account, suffix string, phase Phase, remaining uint64) Candidate {
	return Candidate{ActionID: action(suffix), ExecutionAccountID: account, IntentID: hash("intent-" + suffix),
		MarketManifest: hash("market"), TargetStrategyManifestSHA256: protocol.StrategyManifestSHA256,
		SagaVersion: 8, SpotAmount: "250000", PerpBaseAmount: remaining, Phase: phase}
}

func validQuote(now time.Time, private ed25519.PrivateKey, request protocol.QuoteRequest, candidate Candidate) protocol.QuoteBundle {
	quote := protocol.QuoteBundle{
		SchemaVersion: protocol.QuoteSchemaVersion, RequestID: request.RequestID,
		ExecutionAccountID: request.ExecutionAccountID, SourceEvaluationID: request.SourceEvaluationID,
		MarketManifest:               request.MarketManifest,
		TargetStrategyManifestSHA256: request.TargetStrategyManifestSHA256,
		StrategyVersion:              protocol.StrategyVersion,
		StrategyManifestSHA256:       protocol.StrategyManifestSHA256, SourceConfigSHA256: protocol.SourceConfigSHA256,
		RouteSHA256: protocol.RouteSHA256, OraclePolicySHA256: protocol.OraclePolicySHA256,
		RiskPolicySHA256: protocol.RiskPolicySHA256, Action: protocol.ActionUnwind,
		Source: protocol.SourceIdentity{AdapterID: "reviewed", SpotSource: "rpc", PerpSource: "auth", OracleRound: "101"},
		Spot: protocol.SpotQuote{Venue: protocol.SpotVenue, ChainID: protocol.ChainID,
			SettlementToken: protocol.SettlementToken, StockToken: protocol.StockToken, Router: protocol.Router,
			Side: "sell", SettlementAmount: "25000000", StockAmount: candidate.SpotAmount,
			MinimumAmountOut: "24000000", ReferencePriceMicros: 100_000_000,
			ExpectedUIMultiplier: "500000000000000000", MinOracleRoundID: "101",
			BlockHash: hash("block"), ObservedAtMS: uint64(now.UnixMilli())},
		Perp: protocol.PerpQuote{Venue: protocol.PerpVenue, Symbol: protocol.Symbol, MarketIndex: testMarket,
			Side: "long", ReduceOnly: true, BaseAmount: candidate.PerpBaseAmount, BaseDecimals: 6,
			PriceDecimals: 2, LimitPrice: 101, MarkPrice: 100, Phase: string(candidate.Phase),
			ObservedAtMS: uint64(now.UnixMilli())},
		ExitAuthority: &protocol.ExitQuoteAuthority{Source: "execution-authority", SourceSession: "session-1",
			SourceEventID: "event-" + candidate.ActionID, SourceSequence: 1,
			ExecutionAccountID: candidate.ExecutionAccountID, IntentID: candidate.IntentID,
			MarketManifest: candidate.MarketManifest, PayloadSHA256: digest64('a'),
			ReceivedAtMS: uint64(now.UnixMilli()), SubmissionDeadlineMS: uint64(now.Add(4 * time.Second).UnixMilli()),
			ReconciliationDeadlineMS: uint64(now.Add(24 * time.Hour).UnixMilli())},
		ObservedAtMS: uint64(now.UnixMilli()), ExpiresAtMS: uint64(now.Add(4 * time.Second).UnixMilli()),
	}
	if err := quote.Sign(private); err != nil {
		panic(err)
	}
	return quote
}

func mustPublisher(t *testing.T, store Store, quotes QuoteClient, public ed25519.PublicKey) *Publisher {
	t.Helper()
	publisher, err := New(store, quotes, public, testMarket)
	if err != nil {
		t.Fatal(err)
	}
	publisher.now = func() time.Time { return timelessNow }
	return publisher
}

func keypair(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return private, public
}

func hash(value string) string {
	candidate := Candidate{ExecutionAccountID: "account-hash", IntentID: "0x" + digest64('b'), ActionID: action("f"),
		MarketManifest: "0x" + digest64('d'), TargetStrategyManifestSHA256: digest64('e'),
		SagaVersion: 1, SpotAmount: "1", PerpBaseAmount: 1, Phase: PhasePerpAndSpot}
	return domainHash("test."+value, candidate, 0)
}

func action(value string) string {
	if len(value) == 1 {
		return value + "0000000000000000000000000000000"
	}
	return "f0000000000000000000000000000000"
}

func digest64(value byte) string { return repeat(value, 64) }

func repeat(value byte, count int) string {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return string(result)
}
