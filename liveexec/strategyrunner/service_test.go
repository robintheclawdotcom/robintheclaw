package strategyrunner

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

func TestRunnerEmitsDeterministicCanonicalPairIntent(t *testing.T) {
	service, input := validInput(t, protocol.ActionEntry)
	output, err := service.Run(input)
	if err != nil {
		t.Fatal(err)
	}
	if output.Kind != protocol.ActionEntry || output.PairIntent == nil || output.Unwind != nil {
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
	copy := *intent
	if err := copy.deriveIDs(); err != nil {
		t.Fatal(err)
	}
	if copy.ID != intent.ID || copy.SpotUnwindIntentID != intent.SpotUnwindIntentID || intent.ID == intent.SpotUnwindIntentID {
		t.Fatal("intent ids are not deterministic and domain separated")
	}

	again, err := service.Run(input)
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

func TestRunnerEmitsEpisodeBoundReduceOnlyUnwind(t *testing.T) {
	service, input := validInput(t, protocol.ActionUnwind)
	pairID := testHash("open-pair")
	input.OpenEpisode = &OpenEpisode{
		PairIntentID:               pairID,
		SpotUnwindIntentID:         domainHash(spotUnwindDomain, []byte(pairID)),
		SpotAmount:                 input.Quotes.Spot.StockAmount,
		MinimumSettlementAmountOut: "24000000",
		PerpBaseAmount:             input.Quotes.Perp.BaseAmount,
	}
	output, err := service.Run(input)
	if err != nil {
		t.Fatal(err)
	}
	if output.Kind != protocol.ActionUnwind || output.Unwind == nil || output.PairIntent != nil {
		t.Fatal("runner emitted wrong unwind output type")
	}
	unwind := output.Unwind
	if !unwind.ReduceOnly || unwind.SpotSide != "sell" || unwind.PerpSide != "long" ||
		unwind.PairIntentID != pairID || unwind.SpotUnwindIntentID != input.OpenEpisode.SpotUnwindIntentID || !validHash(unwind.ID) {
		t.Fatal("unwind directive is not bound to the open episode")
	}

	input.OpenEpisode.PerpBaseAmount++
	if _, err := service.Run(input); err == nil {
		t.Fatal("cross-episode quantity substitution accepted")
	}
}

func TestRunnerFailsClosedOnReadinessAndIdentityFailures(t *testing.T) {
	cases := map[string]func(*RunRequest){
		"cross account":           func(input *RunRequest) { input.Readiness.ExecutionAccountID = "account-canary-2" },
		"manifest mismatch":       func(input *RunRequest) { input.AccountState.StrategyManifestSHA256 = testHash("other")[2:] },
		"stale readiness":         func(input *RunRequest) { input.Readiness.ObservedAtMS = 94_999 },
		"unknown order":           func(input *RunRequest) { input.AccountState.UnknownLighterOrders = true },
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
			if _, err := service.Run(input); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}

func TestRunnerRejectsStaleOrTamperedQuotes(t *testing.T) {
	service, input := validInput(t, protocol.ActionEntry)
	input.Quotes.ExpiresAtMS = 100_000
	if _, err := service.Run(input); err == nil {
		t.Fatal("tampered expired quote accepted")
	}

	service, input = validInput(t, protocol.ActionEntry)
	input.Quotes.ExecutionAccountID = "account-canary-2"
	if _, err := service.Run(input); err == nil {
		t.Fatal("tampered account quote accepted")
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
	service, err := NewService(privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.UnixMilli(100_000) }
	quote := protocol.QuoteBundle{
		SchemaVersion:          1,
		RequestID:              testHash("request"),
		ExecutionAccountID:     "account-canary-1",
		SourceEvaluationID:     testHash("evaluation"),
		StrategyVersion:        protocol.StrategyVersion,
		StrategyManifestSHA256: protocol.StrategyManifestSHA256,
		SourceConfigSHA256:     protocol.SourceConfigSHA256,
		RouteSHA256:            protocol.RouteSHA256,
		OraclePolicySHA256:     protocol.OraclePolicySHA256,
		RiskPolicySHA256:       protocol.RiskPolicySHA256,
		Action:                 action,
		Source:                 protocol.SourceIdentity{AdapterID: "reviewed-adapter-v1", SpotSource: "spot", PerpSource: "perp", OracleRound: "round-1"},
		Spot: protocol.SpotQuote{
			Venue: protocol.SpotVenue, ChainID: protocol.ChainID, SettlementToken: protocol.SettlementToken,
			StockToken: protocol.StockToken, Router: protocol.Router, SettlementAmount: "25000000",
			StockAmount: "2000000", ReferencePriceMicros: 25_000_000, BlockHash: testHash("block"), ObservedAtMS: 99_000,
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
		StrategyManifestSHA256: protocol.StrategyManifestSHA256, Lifecycle: "ready", GlobalControl: "active",
		StrategyControl: "active", AccountControl: "active", FullyVerified: true, VaultWired: true, VaultFunded: true,
		ExecutionSignerFunded: true, LighterLinked: true, LighterFunded: true, RouteHealthy: true, OracleHealthy: true,
		SequencerHealthy: true, ObservedAtMS: 99_000,
	}
	state := AccountState{
		ExecutionAccountID: "account-canary-1", AgentID: "agent-canary-1", StrategyManifestSHA256: protocol.StrategyManifestSHA256,
		LighterAccountIndex: 7, LighterAPIKeyIndex: 2, LighterMarketIndex: 101, LighterNonceAligned: true,
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
		quote.Spot.Side = "sell"
		quote.Spot.MinimumAmountOut = "24000000"
		quote.Perp.Side = "long"
		quote.Perp.ReduceOnly = true
		readiness.Lifecycle = "reducing"
		readiness.GlobalControl = "reduce_only"
		readiness.StrategyControl = "reduce_only"
		readiness.AccountControl = "reduce_only"
		state.Flat = false
		state.ActiveEpisodes = 1
	}
	if err := quote.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	return service, RunRequest{
		Evaluation: SourceEvaluation{
			ID: quote.SourceEvaluationID, StrategyVersion: protocol.StrategyVersion, StrategyManifestSHA256: protocol.StrategyManifestSHA256,
			SourceConfigSHA256: protocol.SourceConfigSHA256, DatasetManifest: testHash("dataset"), MarketManifest: testHash("market"),
			Status: "approved", Action: action, ObservedAtMS: 99_000, EstimatedCostMicros: 10_000,
		},
		Readiness: readiness, AccountState: state, Quotes: quote,
	}
}

func testHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "0x" + hex.EncodeToString(digest[:])
}
