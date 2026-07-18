package strategyrunner

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

type fakeDispatcher struct {
	intents []PairIntent
	exits   []ExitSubmission
	err     error
}

func (f *fakeDispatcher) SubmitIntent(_ context.Context, intent PairIntent) (IntentPersistence, error) {
	f.intents = append(f.intents, intent)
	if f.err != nil {
		return IntentPersistence{}, f.err
	}
	return IntentPersistence{Status: "persisted", IntentID: intent.ID, CoordinatorState: "prechecked", CoordinatorVersion: 1}, nil
}

func (f *fakeDispatcher) SubmitExit(_ context.Context, exit ExitSubmission) (ExitPersistence, error) {
	f.exits = append(f.exits, exit)
	if f.err != nil {
		return ExitPersistence{}, f.err
	}
	return ExitPersistence{
		Status: "persisted", RequestID: exit.RequestID, IntentID: exit.IntentID,
		CoordinatorState: "unwinding", CoordinatorVersion: 7,
	}, nil
}

func TestRunnerEmitsDeterministicCanonicalPairIntent(t *testing.T) {
	service, input := validInput(t, protocol.ActionEntry)
	output, err := service.Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if output.Kind != protocol.ActionEntry || output.PairIntent == nil || output.Unwind != nil || output.Persistence == nil {
		t.Fatal("runner emitted wrong output type")
	}
	intent := output.PairIntent
	if intent.StrategyManifestSHA256 != protocol.StrategyManifestSHA256 || intent.RiskVersion != protocol.RiskVersion ||
		intent.SourceEvaluationID != input.Evaluation.ID || intent.ExecutionAccountID != input.AccountState.ExecutionAccountID {
		t.Fatal("intent omitted immutable strategy or account identity")
	}
	if intent.SpotNotionalMicros != 25_000_000 || intent.PerpNotionalMicros != 25_000_000 || intent.LeverageMicros != 1_000_000 ||
		intent.SpotSide != "buy" || intent.PerpSide != "short" || intent.MaxUnwindAttempts != 3 {
		t.Fatal("intent did not apply the fixed risk policy")
	}
	if output.Persistence.IntentID != intent.ID || len(service.dispatcher.(*fakeDispatcher).intents) != 1 {
		t.Fatal("runner reported success without coordinator persistence")
	}
	copy := *intent
	if err := copy.deriveIDs(); err != nil {
		t.Fatal(err)
	}
	if copy.ID != intent.ID || copy.SpotUnwindIntentID != intent.SpotUnwindIntentID || intent.ID == intent.SpotUnwindIntentID {
		t.Fatal("intent ids are not deterministic and domain separated")
	}

	service.now = func() time.Time { return time.UnixMilli(101_000) }
	again, err := service.Run(context.Background(), input)
	if err != nil || again.PairIntent.ID != intent.ID {
		t.Fatal("same frozen evidence produced a different intent")
	}
	fixturePath := filepath.Join("..", "testdata", "pair-intent-v2.json")
	fixtureJSON, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture PairIntent
	if err := json.Unmarshal(fixtureJSON, &fixture); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*intent, fixture) {
		t.Fatal("runner output drifted from the Rust-validated PairIntent fixture")
	}
}

func TestRunnerNeverReportsUnpersistedIntent(t *testing.T) {
	service, input := validInput(t, protocol.ActionEntry)
	dispatcher := service.dispatcher.(*fakeDispatcher)
	dispatcher.err = ErrCoordinatorAmbiguous
	output, err := service.Run(context.Background(), input)
	if !errors.Is(err, ErrCoordinatorAmbiguous) || output.PairIntent != nil || output.Persistence != nil || len(dispatcher.intents) != 1 {
		t.Fatalf("unpersisted intent was reported as success: output=%+v err=%v", output, err)
	}
}

func TestRunnerAcceptsHedgedLegsBelowTheCanaryCeiling(t *testing.T) {
	service, input := validInput(t, protocol.ActionEntry)
	input.Quotes.Perp.LimitPrice = 24_999
	input.Quotes.Perp.MarkPrice = 24_999
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	if err := input.Quotes.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	output, err := service.Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if output.PairIntent == nil || output.PairIntent.SpotNotionalMicros != 25_000_000 ||
		output.PairIntent.PerpNotionalMicros != 24_999_000 {
		t.Fatalf("sub-cap notional was not preserved: %+v", output.PairIntent)
	}
}

func TestRunnerEmitsEpisodeBoundReduceOnlyUnwind(t *testing.T) {
	service, input := validInput(t, protocol.ActionUnwind)
	pairID := testHash("open-pair")
	input.OpenEpisode = openEpisode(input)
	directive, err := buildUnwind(input)
	if err != nil {
		t.Fatal(err)
	}
	unwind := &directive
	if !unwind.ReduceOnly || unwind.SpotSide != "sell" || unwind.PerpSide != "long" ||
		unwind.PairIntentID != pairID || unwind.SpotUnwindIntentID != input.OpenEpisode.SpotUnwindIntentID || !validHash(unwind.ID) {
		t.Fatal("unwind directive is not bound to the open episode")
	}

	output, err := service.Run(context.Background(), input)
	if err != nil || output.Unwind == nil || output.ExitPersistence == nil || len(service.dispatcher.(*fakeDispatcher).exits) != 1 {
		t.Fatalf("unwind was not durably dispatched: output=%+v err=%v", output, err)
	}
	input.OpenEpisode.PerpBaseAmount++
	if _, err := buildUnwind(input); err == nil {
		t.Fatal("cross-episode quantity substitution accepted")
	}
}

func TestRunnerDispatchesNaturalPauseAndCloseExits(t *testing.T) {
	tests := []struct {
		lifecycle string
		reason    string
	}{
		{lifecycle: "running", reason: "strategy_exit"},
		{lifecycle: "reducing", reason: "operator_exit"},
		{lifecycle: "closing", reason: "operator_exit"},
	}
	for _, test := range tests {
		t.Run(test.lifecycle, func(t *testing.T) {
			service, input := validInput(t, protocol.ActionUnwind)
			input.Readiness.Lifecycle = test.lifecycle
			input.OpenEpisode = openEpisode(input)
			if _, err := service.Run(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			exits := service.dispatcher.(*fakeDispatcher).exits
			if len(exits) != 1 || exits[0].Reason != test.reason {
				t.Fatalf("wrong exit dispatch for %s: %+v", test.lifecycle, exits)
			}
		})
	}
}

func TestRunnerDispatchesBoundedRepairQuantity(t *testing.T) {
	service, input := validInput(t, protocol.ActionUnwind)
	input.Quotes.Perp.BaseAmount = 250_000
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	if err := input.Quotes.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	input.OpenEpisode = openEpisode(input)
	output, err := service.Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if output.Unwind == nil || output.Unwind.PerpBaseAmount != 250_000 {
		t.Fatalf("bounded repair quantity was not preserved: %+v", output.Unwind)
	}
}

func TestRunnerFailsClosedOnReadinessAndIdentityFailures(t *testing.T) {
	cases := map[string]func(*RunRequest){
		"cross account":           func(input *RunRequest) { input.Readiness.ExecutionAccountID = "account-canary-2" },
		"manifest mismatch":       func(input *RunRequest) { input.AccountState.StrategyManifestSHA256 = testHash("other")[2:] },
		"stale readiness":         func(input *RunRequest) { input.Readiness.ObservedAtMS = 94_999 },
		"unknown order":           func(input *RunRequest) { input.AccountState.UnknownLighterOrders = true },
		"reserved api key":        func(input *RunRequest) { input.AccountState.LighterAPIKeyIndex = 3 },
		"nonce drift":             func(input *RunRequest) { input.AccountState.LighterNonceAligned = false },
		"oracle down":             func(input *RunRequest) { input.Readiness.OracleHealthy = false },
		"sequencer down":          func(input *RunRequest) { input.Readiness.SequencerHealthy = false },
		"insufficient margin":     func(input *RunRequest) { input.AccountState.CollateralMicros = 19_999_999 },
		"active episode":          func(input *RunRequest) { input.AccountState.ActiveEpisodes = 1 },
		"daily turnover consumed": func(input *RunRequest) { input.AccountState.DailyTurnoverMicros = 1 },
		"user strategy":           func(input *RunRequest) { input.Evaluation.StrategyVersion = "custom" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			service, input := validInput(t, protocol.ActionEntry)
			mutate(&input)
			if _, err := service.Run(context.Background(), input); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}

func TestRunnerRejectsStaleOrTamperedQuotes(t *testing.T) {
	service, input := validInput(t, protocol.ActionEntry)
	input.Quotes.ExpiresAtMS = 100_000
	if _, err := service.Run(context.Background(), input); err == nil {
		t.Fatal("tampered expired quote accepted")
	}

	service, input = validInput(t, protocol.ActionEntry)
	input.Quotes.ExecutionAccountID = "account-canary-2"
	if _, err := service.Run(context.Background(), input); err == nil {
		t.Fatal("tampered account quote accepted")
	}

	service, input = validInput(t, protocol.ActionUnwind)
	input.OpenEpisode = openEpisode(input)
	input.Quotes.TargetStrategyManifestSHA256 = protocol.PreviousStrategyManifestSHA256
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	if err := input.Quotes.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background(), input); err == nil {
		t.Fatal("normal runner accepted a predecessor-manifest recovery quote")
	}
}

func TestRunnerRejectsAccountAndQuoteAgreementOnUnreviewedMarket(t *testing.T) {
	service, input := validInput(t, protocol.ActionEntry)
	input.Quotes.Perp.MarketIndex = 102
	input.AccountState.LighterMarketIndex = 102
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	if err := input.Quotes.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background(), input); err == nil {
		t.Fatal("account and quote agreement bypassed the reviewed market pin")
	}
}

func TestRunRequestRejectsStrategyParameters(t *testing.T) {
	_, input := validInput(t, protocol.ActionEntry)
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(bytes.TrimSuffix(encoded, []byte("}")), []byte(`,"leverage":2}`)...)
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var parsed RunRequest
	if err := decoder.Decode(&parsed); err == nil {
		t.Fatal("user-supplied strategy parameter accepted")
	}
}

func validInput(t *testing.T, action protocol.Action) (*Service, RunRequest) {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	service, err := NewService(privateKey.Public().(ed25519.PublicKey), &fakeDispatcher{}, 101)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.UnixMilli(100_000) }
	quote := protocol.QuoteBundle{
		SchemaVersion:          protocol.QuoteSchemaVersion,
		RequestID:              testHash("request"),
		ExecutionAccountID:     "account-canary-1",
		SourceEvaluationID:     testHash("evaluation"),
		MarketManifest:         testHash("market"),
		StrategyVersion:        protocol.StrategyVersion,
		StrategyManifestSHA256: protocol.StrategyManifestSHA256,
		SourceConfigSHA256:     protocol.SourceConfigSHA256,
		RouteSHA256:            protocol.RouteSHA256,
		OraclePolicySHA256:     protocol.OraclePolicySHA256,
		RiskPolicySHA256:       protocol.RiskPolicySHA256,
		Action:                 action,
		Source:                 protocol.SourceIdentity{AdapterID: "reviewed-adapter-v1", SpotSource: "spot", PerpSource: "perp", OracleRound: "101"},
		Spot: protocol.SpotQuote{
			Venue: protocol.SpotVenue, ChainID: protocol.ChainID, SettlementToken: protocol.SettlementToken,
			StockToken: protocol.StockToken, Router: protocol.Router, SettlementAmount: "25000000",
			StockAmount: "2000000", ReferencePriceMicros: 25_000_000, BlockHash: testHash("block"), ObservedAtMS: 99_000,
			ExpectedUIMultiplier: "500000000000000000", MinOracleRoundID: "101",
		},
		Perp: protocol.PerpQuote{
			Venue: protocol.PerpVenue, Symbol: protocol.Symbol, MarketIndex: 101,
			BaseAmount: 1_000_000, BaseDecimals: 6, PriceDecimals: 3, LimitPrice: 25_000, MarkPrice: 25_000, ObservedAtMS: 99_000,
		},
		ObservedAtMS: 99_000,
		ExpiresAtMS:  102_000,
	}
	readiness := Readiness{
		ExecutionAccountID: "account-canary-1", AgentID: "agent-canary-1", StrategyVersion: protocol.StrategyVersion,
		StrategyManifestSHA256: protocol.StrategyManifestSHA256, Lifecycle: "ready", GlobalControl: "ACTIVE",
		StrategyControl: "ACTIVE", AccountControl: "ACTIVE", FullyVerified: true, VaultWired: true, VaultFunded: true,
		ExecutionSignerFunded: true, LighterLinked: true, LighterFunded: true, RouteHealthy: true, OracleHealthy: true,
		SequencerHealthy: true, ObservedAtMS: 99_000,
	}
	state := AccountState{
		ExecutionAccountID: "account-canary-1", AgentID: "agent-canary-1", StrategyManifestSHA256: protocol.StrategyManifestSHA256,
		LighterAccountIndex: 7, LighterAPIKeyIndex: 4, LighterMarketIndex: 101, LighterNonceAligned: true,
		CollateralMicros: 20_000_000, MaintenanceMarginMicros: 10_000_000,
		RobinhoodVault: "0x0000000000000000000000000000000000000002", RobinhoodSigner: "0x0000000000000000000000000000000000000003",
		RobinhoodNonceAligned: true, NAVMicros: 100_000_000, Flat: true, SpotDecimals: 6, SpotConfigVersion: 1,
		UIMultiplierE18: "500000000000000000", NextClientOrderIndex: 1, NextUnwindOrderIndex: 2, ObservedAtMS: 99_000,
	}
	if action == protocol.ActionEntry {
		quote.Spot.Side = "buy"
		quote.Spot.MinimumAmountOut = "1990000"
		quote.Perp.Side = "short"
	} else {
		quote.TargetStrategyManifestSHA256 = protocol.StrategyManifestSHA256
		quote.Spot.Side = "sell"
		quote.Spot.MinimumAmountOut = "24000000"
		quote.Perp.Side = "long"
		quote.Perp.ReduceOnly = true
		quote.Perp.Phase = "perp_and_spot"
		readiness.Lifecycle = "reducing"
		readiness.GlobalControl = "REDUCE_ONLY"
		readiness.StrategyControl = "REDUCE_ONLY"
		readiness.AccountControl = "REDUCE_ONLY"
		state.Flat = false
		state.ActiveEpisodes = 1
		quote.ExitAuthority = &protocol.ExitQuoteAuthority{
			Source: "execution-authority", SourceSession: "authority-session-1", SourceEventID: "authority-event-1",
			SourceSequence: 1, ExecutionAccountID: quote.ExecutionAccountID, IntentID: testHash("open-pair"),
			MarketManifest: quote.MarketManifest, PayloadSHA256: strings.Repeat("a", 64), ReceivedAtMS: 99_500,
			SubmissionDeadlineMS:     quote.ExpiresAtMS,
			ReconciliationDeadlineMS: quote.ExpiresAtMS + protocol.MaximumExitReconciliationMS,
		}
	}
	if err := quote.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	return service, RunRequest{
		Evaluation: SourceEvaluation{
			ID: quote.SourceEvaluationID, StrategyVersion: protocol.StrategyVersion, StrategyManifestSHA256: protocol.StrategyManifestSHA256,
			SourceConfigSHA256: protocol.SourceConfigSHA256, DatasetManifest: testHash("dataset"), MarketManifest: testHash("market"),
			Status: "approved", Action: action, ObservedAtMS: 99_000, EstimatedCostMicros: 10_000,
			SourceEpisodeID:   "33333333-3333-4333-8333-333333333333",
			PaperEvaluationID: "44444444-4444-4444-8444-444444444444",
			PairIntentID:      map[bool]string{true: testHash("open-pair"), false: ""}[action == protocol.ActionUnwind],
		},
		Readiness: readiness, AccountState: state, Quotes: quote,
	}
}

func openEpisode(input RunRequest) *OpenEpisode {
	pairID := testHash("open-pair")
	return &OpenEpisode{
		PairIntentID:               pairID,
		SpotUnwindIntentID:         domainHash(spotUnwindDomain, []byte(pairID)),
		SpotAmount:                 input.Quotes.Spot.StockAmount,
		MinimumSettlementAmountOut: "24000000",
		PerpBaseAmount:             input.Quotes.Perp.BaseAmount,
	}
}

func testHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "0x" + hex.EncodeToString(digest[:])
}
