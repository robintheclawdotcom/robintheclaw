package scheduler

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

const testMarket = uint32(17)

type memoryStore struct {
	dispatches  []*Dispatch
	states      map[string]string
	eligible    func(string, int) bool
	checks      map[string]int
	blockReason map[string]string
}

func newMemoryStore(dispatches ...*Dispatch) *memoryStore {
	store := &memoryStore{dispatches: dispatches, states: map[string]string{}, checks: map[string]int{}, blockReason: map[string]string{}}
	for _, dispatch := range dispatches {
		store.states[store.key(*dispatch)] = "pending"
	}
	return store
}

func (s *memoryStore) key(d Dispatch) string { return d.EvaluationID + "/" + d.ExecutionAccountID }

func (s *memoryStore) Claim(_ context.Context, _ time.Time, _ time.Duration) (*Dispatch, error) {
	for _, dispatch := range s.dispatches {
		state := s.states[s.key(*dispatch)]
		if state == "pending" || state == "ambiguous" {
			s.states[s.key(*dispatch)] = "running"
			copy := *dispatch
			copy.QuoteBody = append([]byte(nil), dispatch.QuoteBody...)
			copy.RunnerBody = append([]byte(nil), dispatch.RunnerBody...)
			return &copy, nil
		}
	}
	return nil, nil
}

func (s *memoryStore) Eligible(_ context.Context, d Dispatch) (bool, error) {
	key := s.key(d)
	s.checks[key]++
	if s.eligible != nil {
		return s.eligible(key, s.checks[key]), nil
	}
	return true, nil
}

func (s *memoryStore) PrepareQuote(_ context.Context, d Dispatch, requestID string, requestedAt uint64) error {
	target := s.find(d)
	target.RequestID, target.RequestedAtMS = requestID, requestedAt
	return nil
}

func (s *memoryStore) SaveQuote(_ context.Context, d Dispatch, body []byte, sha string) error {
	target := s.find(d)
	target.QuoteBody, target.QuoteSHA256 = append([]byte(nil), body...), sha
	return nil
}

func (s *memoryStore) SaveRunner(_ context.Context, d Dispatch, body []byte, sha string) error {
	target := s.find(d)
	target.RunnerBody, target.RunnerSHA256 = append([]byte(nil), body...), sha
	return nil
}

func (s *memoryStore) Complete(_ context.Context, d Dispatch, _ []byte, _ string) error {
	s.states[s.key(d)] = "succeeded"
	return nil
}

func (s *memoryStore) Ambiguous(_ context.Context, d Dispatch, _ []byte, _ string) error {
	s.states[s.key(d)] = "ambiguous"
	return nil
}

func (s *memoryStore) Retry(_ context.Context, d Dispatch, _ string) error {
	s.states[s.key(d)] = "pending"
	return nil
}

func (s *memoryStore) Block(_ context.Context, d Dispatch, reason string) error {
	key := s.key(d)
	s.states[key], s.blockReason[key] = "blocked", reason
	return nil
}

func (s *memoryStore) find(d Dispatch) *Dispatch {
	for _, candidate := range s.dispatches {
		if s.key(*candidate) == s.key(d) {
			return candidate
		}
	}
	panic("missing dispatch")
}

type quoteStub struct {
	private  ed25519.PrivateKey
	now      time.Time
	mutate   func(*QuoteBundle)
	requests [][]byte
}

func (q *quoteStub) Quote(_ context.Context, body []byte) ([]byte, error) {
	q.requests = append(q.requests, append([]byte(nil), body...))
	var request QuoteRequest
	if err := decodeStrict(body, &request); err != nil {
		return nil, err
	}
	spotSide, perpSide, phase, reduceOnly := "buy", "short", "", false
	var exitAuthority json.RawMessage
	if request.Action == ActionUnwind {
		spotSide, perpSide, phase, reduceOnly = "sell", "long", "perp_and_spot", true
		exitAuthority = mustJSON(map[string]any{
			"execution_account_id": request.ExecutionAccountID,
			"intent_id":            request.IntentID,
		})
	}
	quote := QuoteBundle{
		SchemaVersion: quoteSchemaVersion, RequestID: request.RequestID, ExecutionAccountID: request.ExecutionAccountID,
		SourceEvaluationID: request.SourceEvaluationID, MarketManifest: request.MarketManifest, StrategyVersion: StrategyVersion,
		StrategyManifestSHA256: StrategyManifestSHA256, SourceConfigSHA256: SourceConfigSHA256, RouteSHA256: routeSHA256,
		OraclePolicySHA256: oraclePolicySHA256, RiskPolicySHA256: riskPolicySHA256, Action: request.Action,
		Source: json.RawMessage(`{"adapter_id":"reviewed","spot_source":"rpc","perp_source":"auth","oracle_round":"round"}`),
		Spot: mustJSON(spotQuote{Venue: "robinhood-chain-mainnet", Side: spotSide, StockAmount: "1", MinimumAmountOut: "1",
			ExpectedUIMultiplier: "1000000000000000000", MinOracleRoundID: "1"}),
		Perp: mustJSON(perpQuote{Venue: "lighter-mainnet", Symbol: "AAPL", MarketIndex: testMarket, Side: perpSide,
			ReduceOnly: reduceOnly, Phase: phase, BaseAmount: 1, BaseDecimals: 6, PriceDecimals: 2,
			LimitPrice: 100, MarkPrice: 100, ObservedAtMS: uint64(q.now.UnixMilli())}),
		ExitAuthority: exitAuthority,
		ObservedAtMS:  uint64(q.now.UnixMilli()), ExpiresAtMS: uint64(q.now.Add(4 * time.Second).UnixMilli()),
		PublicKey: base64.StdEncoding.EncodeToString(q.private.Public().(ed25519.PublicKey)),
	}
	if q.mutate != nil {
		q.mutate(&quote)
	}
	id, err := quote.calculateID()
	if err != nil {
		return nil, err
	}
	quote.ID = id
	material, err := quote.signatureMaterial()
	if err != nil {
		return nil, err
	}
	quote.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(q.private, material))
	return json.Marshal(quote)
}

type runnerStub struct {
	bodies   [][]byte
	errors   []error
	mismatch bool
}

func (r *runnerStub) Run(_ context.Context, body []byte) ([]byte, error) {
	r.bodies = append(r.bodies, append([]byte(nil), body...))
	if len(r.errors) != 0 {
		err := r.errors[0]
		r.errors = r.errors[1:]
		if err != nil {
			return nil, err
		}
	}
	var request RunRequest
	if err := decodeStrict(body, &request); err != nil {
		return nil, err
	}
	evaluationID := request.Evaluation.ID
	if r.mismatch {
		evaluationID = testHash("wrong-evaluation")
	}
	intentID := testHash(request.AccountState.ExecutionAccountID + "-intent")
	if request.Evaluation.Action == ActionUnwind {
		unwindID := testHash(request.AccountState.ExecutionAccountID + "-unwind")
		return json.Marshal(map[string]any{
			"kind": ActionUnwind,
			"unwind": map[string]any{
				"id": unwindID, "pair_intent_id": request.Evaluation.PairIntentID,
				"execution_account_id":     request.AccountState.ExecutionAccountID,
				"agent_id":                 request.AccountState.AgentID,
				"source_evaluation_id":     evaluationID,
				"strategy_manifest_sha256": StrategyManifestSHA256,
			},
			"exit_persistence": map[string]any{
				"status": "persisted", "request_id": unwindID,
				"intent_id":         request.Evaluation.PairIntentID,
				"coordinator_state": "EXITING", "coordinator_version": 5,
			},
		})
	}
	return json.Marshal(map[string]any{
		"kind": ActionEntry,
		"pair_intent": map[string]any{
			"id": intentID, "execution_account_id": request.AccountState.ExecutionAccountID,
			"agent_id": request.AccountState.AgentID, "source_evaluation_id": evaluationID,
			"strategy_manifest_sha256": StrategyManifestSHA256,
		},
		"persistence": map[string]any{
			"status": "persisted", "intent_id": intentID, "coordinator_state": "ENTRY_PENDING", "coordinator_version": 1,
		},
	})
}

func TestSchedulerDispatchesTwoTenants(t *testing.T) {
	now, private, public := testClockAndKey(t)
	first := validDispatch(t, now, "account-one", "agent-one")
	second := validDispatch(t, now, "account-two", "agent-two")
	store := newMemoryStore(first, second)
	quotes := &quoteStub{private: private, now: now}
	runner := &runnerStub{}
	service := mustScheduler(t, store, quotes, runner, public, now)
	for range 2 {
		if err := service.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if store.states[store.key(*first)] != "succeeded" || store.states[store.key(*second)] != "succeeded" {
		t.Fatalf("unexpected states: %#v", store.states)
	}
	if len(quotes.requests) != 2 || len(runner.bodies) != 2 || reflect.DeepEqual(runner.bodies[0], runner.bodies[1]) {
		t.Fatal("tenant dispatches were not independent")
	}
}

func TestSchedulerDispatchesNaturalStrategyExit(t *testing.T) {
	now, private, public := testClockAndKey(t)
	dispatch := validDispatch(t, now, "account-exit", "agent-exit")
	dispatch.Evaluation.Action = ActionUnwind
	dispatch.Evaluation.PairIntentID = testHash("account-exit-pair")
	dispatch.AccountState.Flat = false
	dispatch.AccountState.ActiveEpisodes = 1
	dispatch.OpenEpisode = &OpenEpisode{
		PairIntentID: dispatch.Evaluation.PairIntentID, SpotUnwindIntentID: testHash("spot-unwind"),
		SpotAmount: "1", MinimumSettlementAmountOut: "1", PerpBaseAmount: 1,
	}
	sealApproval(t, dispatch)
	store := newMemoryStore(dispatch)
	quotes := &quoteStub{private: private, now: now}
	runner := &runnerStub{}
	service := mustScheduler(t, store, quotes, runner, public, now)
	if err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.states[store.key(*dispatch)] != "succeeded" || len(quotes.requests) != 1 || len(runner.bodies) != 1 {
		t.Fatal("natural exit was not dispatched exactly once")
	}
	var request QuoteRequest
	if err := decodeStrict(quotes.requests[0], &request); err != nil || request.Action != ActionUnwind ||
		request.IntentID != dispatch.Evaluation.PairIntentID {
		t.Fatal("natural exit quote was not bound to the pair intent")
	}
	var run RunRequest
	if err := decodeStrict(runner.bodies[0], &run); err != nil || run.OpenEpisode == nil ||
		run.OpenEpisode.PairIntentID != dispatch.Evaluation.PairIntentID {
		t.Fatal("strategy runner did not receive the immutable open episode")
	}
}

func TestSchedulerReusesExactEvidenceAfterAmbiguousRestart(t *testing.T) {
	now, private, public := testClockAndKey(t)
	dispatch := validDispatch(t, now, "account-retry", "agent-retry")
	store := newMemoryStore(dispatch)
	quotes := &quoteStub{private: private, now: now}
	firstRunner := &runnerStub{errors: []error{&ResponseError{Status: 502, Body: []byte(`{"error":"ambiguous"}`)}}}
	first := mustScheduler(t, store, quotes, firstRunner, public, now)
	if err := first.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.states[store.key(*dispatch)] != "ambiguous" {
		t.Fatalf("expected ambiguous state, got %s", store.states[store.key(*dispatch)])
	}
	secondRunner := &runnerStub{}
	restarted := mustScheduler(t, store, quotes, secondRunner, public, now.Add(time.Millisecond))
	if err := restarted.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(quotes.requests) != 1 {
		t.Fatalf("restart requested a replacement quote: %d calls", len(quotes.requests))
	}
	if len(firstRunner.bodies) != 1 || len(secondRunner.bodies) != 1 || !reflect.DeepEqual(firstRunner.bodies[0], secondRunner.bodies[0]) {
		t.Fatal("restart did not reuse the exact persisted runner request")
	}
}

func TestSchedulerRejectsStaleAndUnpromotedEvaluation(t *testing.T) {
	now, private, public := testClockAndKey(t)
	tests := []struct {
		name     string
		mutate   func(*Dispatch)
		eligible bool
	}{
		{name: "stale", mutate: func(d *Dispatch) {
			d.Evaluation.ObservedAtMS = uint64(now.Add(-6 * time.Second).UnixMilli())
			sealApproval(t, d)
		}, eligible: true},
		{name: "unpromoted", mutate: func(*Dispatch) {}, eligible: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dispatch := validDispatch(t, now, "account-"+tt.name, "agent-"+tt.name)
			tt.mutate(dispatch)
			store := newMemoryStore(dispatch)
			store.eligible = func(string, int) bool { return tt.eligible }
			quotes := &quoteStub{private: private, now: now}
			service := mustScheduler(t, store, quotes, &runnerStub{}, public, now)
			if err := service.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if store.states[store.key(*dispatch)] != "blocked" || len(quotes.requests) != 0 {
				t.Fatalf("dispatch did not fail closed: state=%s quotes=%d", store.states[store.key(*dispatch)], len(quotes.requests))
			}
		})
	}
}

func TestSchedulerStopsWhenAccountBlockedMidFlight(t *testing.T) {
	now, private, public := testClockAndKey(t)
	dispatch := validDispatch(t, now, "account-blocked", "agent-blocked")
	store := newMemoryStore(dispatch)
	store.eligible = func(_ string, check int) bool { return check == 1 }
	quotes := &quoteStub{private: private, now: now}
	runner := &runnerStub{}
	service := mustScheduler(t, store, quotes, runner, public, now)
	if err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(quotes.requests) != 1 || len(runner.bodies) != 0 || store.states[store.key(*dispatch)] != "blocked" {
		t.Fatal("mid-flight block reached the strategy runner")
	}
}

func TestSchedulerRejectsQuoteAndRunnerIdentityMismatch(t *testing.T) {
	now, private, public := testClockAndKey(t)
	t.Run("quote", func(t *testing.T) {
		dispatch := validDispatch(t, now, "account-quote", "agent-quote")
		store := newMemoryStore(dispatch)
		quotes := &quoteStub{private: private, now: now, mutate: func(q *QuoteBundle) { q.ExecutionAccountID = "account-other" }}
		service := mustScheduler(t, store, quotes, &runnerStub{}, public, now)
		if err := service.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if store.states[store.key(*dispatch)] != "blocked" {
			t.Fatal("quote mismatch was not blocked")
		}
	})
	t.Run("runner", func(t *testing.T) {
		dispatch := validDispatch(t, now, "account-runner", "agent-runner")
		store := newMemoryStore(dispatch)
		service := mustScheduler(t, store, &quoteStub{private: private, now: now}, &runnerStub{mismatch: true}, public, now)
		if err := service.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if store.states[store.key(*dispatch)] != "blocked" {
			t.Fatal("runner mismatch was not blocked")
		}
	})
}

func TestApprovalSchemaRejectsUserParameters(t *testing.T) {
	dispatch := validDispatch(t, time.Unix(1_800_000_000, 0), "account-schema", "agent-schema")
	body, err := json.Marshal(dispatch.Evaluation)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.TrimSuffix(string(body), "}") + `,"leverage":4,"calldata":"0x01"}`)
	var evaluation SourceEvaluation
	if err := decodeStrict(body, &evaluation); err == nil {
		t.Fatal("user-supplied strategy parameters were accepted")
	}
	runBody, err := json.Marshal(RunRequest{Evaluation: dispatch.Evaluation, Readiness: dispatch.Readiness, AccountState: dispatch.AccountState, Quotes: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"calldata", "private_key", "kms", "leverage"} {
		if strings.Contains(string(runBody), forbidden) {
			t.Fatalf("runner request contains forbidden field %q", forbidden)
		}
	}
}

func TestSchedulerPinsReviewedLighterMarket(t *testing.T) {
	now, private, public := testClockAndKey(t)
	dispatch := validDispatch(t, now, "account-market", "agent-market")
	dispatch.AccountState.LighterMarketIndex++
	sealApproval(t, dispatch)
	store := newMemoryStore(dispatch)
	service := mustScheduler(t, store, &quoteStub{private: private, now: now}, &runnerStub{}, public, now)
	if err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.states[store.key(*dispatch)] != "blocked" {
		t.Fatal("unreviewed Lighter market was not blocked")
	}
}

func TestSchedulerRejectsReservedLighterAPIKey(t *testing.T) {
	now, private, public := testClockAndKey(t)
	dispatch := validDispatch(t, now, "account-api-key", "agent-api-key")
	dispatch.AccountState.LighterAPIKeyIndex = 3
	sealApproval(t, dispatch)
	store := newMemoryStore(dispatch)
	service := mustScheduler(t, store, &quoteStub{private: private, now: now}, &runnerStub{}, public, now)
	if err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.states[store.key(*dispatch)] != "blocked" {
		t.Fatal("reserved Lighter API key was not blocked")
	}
}

func validDispatch(t *testing.T, now time.Time, account, agent string) *Dispatch {
	t.Helper()
	dispatch := &Dispatch{
		EvaluationID: testHash(account + "-evaluation"), ExecutionAccountID: account, AgentID: agent,
		ExpiresAt: now.Add(5 * time.Second),
		Evaluation: SourceEvaluation{
			ID: testHash(account + "-evaluation"), StrategyVersion: StrategyVersion, StrategyManifestSHA256: StrategyManifestSHA256,
			SourceConfigSHA256: SourceConfigSHA256, DatasetManifest: testHash("dataset"), MarketManifest: testHash("market"),
			Status: "approved", Action: ActionEntry, ObservedAtMS: uint64(now.UnixMilli()), EstimatedCostMicros: 1,
			SourceEpisodeID:   "33333333-3333-4333-8333-333333333333",
			PaperEvaluationID: "44444444-4444-4444-8444-444444444444",
		},
		Readiness: Readiness{
			ExecutionAccountID: account, AgentID: agent, StrategyVersion: StrategyVersion, StrategyManifestSHA256: StrategyManifestSHA256,
			Lifecycle: "running", GlobalControl: "ACTIVE", StrategyControl: "ACTIVE", AccountControl: "ACTIVE",
			FullyVerified: true, VaultWired: true, VaultFunded: true, ExecutionSignerFunded: true, LighterLinked: true,
			LighterFunded: true, RouteHealthy: true, OracleHealthy: true, SequencerHealthy: true, ObservedAtMS: uint64(now.UnixMilli()),
		},
		AccountState: AccountState{
			ExecutionAccountID: account, AgentID: agent, StrategyManifestSHA256: StrategyManifestSHA256,
			LighterAccountIndex: 1, LighterAPIKeyIndex: 4, LighterMarketIndex: testMarket, LighterNonceAligned: true,
			CollateralMicros: 20_000_000, MaintenanceMarginMicros: 10_000_000,
			RobinhoodVault: "0x1111111111111111111111111111111111111111", RobinhoodSigner: "0x2222222222222222222222222222222222222222",
			RobinhoodNonceAligned: true, NAVMicros: 50_000_000, Flat: true, SpotDecimals: 18, SpotConfigVersion: 1,
			UIMultiplierE18: "1000000000000000000", NextClientOrderIndex: 1, NextUnwindOrderIndex: 2, ObservedAtMS: uint64(now.UnixMilli()),
		},
	}
	sealApproval(t, dispatch)
	return dispatch
}

func sealApproval(t *testing.T, dispatch *Dispatch) {
	t.Helper()
	material, err := approvalMaterial(*dispatch)
	if err != nil {
		t.Fatal(err)
	}
	dispatch.ApprovalSHA256 = digest(material)
}

func mustScheduler(t *testing.T, store Store, quotes QuoteClient, runner RunnerClient, public ed25519.PublicKey, now time.Time) *Scheduler {
	t.Helper()
	service, err := New(store, quotes, runner, public, testMarket, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	return service
}

func testClockAndKey(t *testing.T) (time.Time, ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return time.Unix(1_800_000_000, 0).UTC(), private, public
}

func testHash(value string) string { return fmt.Sprintf("0x%064x", []byte(value))[:66] }

func mustJSON(value any) json.RawMessage {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}
