package evaluation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validCandidate(now time.Time) PaperCandidate {
	timestamp := now.Add(-time.Second).UnixMilli()
	return PaperCandidate{
		EvaluationID:  "11111111-1111-4111-8111-111111111111",
		EventID:       "22222222-2222-4222-8222-222222222222",
		EpisodeID:     "33333333-3333-4333-8333-333333333333",
		SourceSession: "lighter-session-1",
		SourceEventID: "lighter-event-1",
		Symbol:        Symbol,
		Status:        "candidate",
		Direction:     Direction,
		BlockNumber:   99,
		BlockHash:     "0x" + strings.Repeat("1", 64),
		GrossEdgePPM:  4_000,
		NetEdgePPM:    2_000,
		EvaluatedAt:   now.Add(-time.Second),
		Evidence: PaperEvidence{
			Direction:             Direction,
			TickerSourceSession:   "lighter-session-1",
			TickerSourceEventID:   "lighter-event-1",
			TickerTimestampMS:     &timestamp,
			TickerReceivedAt:      now.Add(-time.Second),
			PerpBidPrice:          "201.25",
			PerpBidSize:           "4.5",
			PerpPriceMicros:       "201250000",
			PerpBidSizeSharesWei:  "4500000000000000000",
			SettlementAmountInRaw: "25000000",
			StockAmountOutRaw:     "125000000000000000",
			UnderlyingSharesWei:   "125000000000000000",
			SettlementDecimals:    6,
			StockDecimals:         18,
			UIMultiplierRaw:       "1000000000000000000",
			NewUIMultiplierRaw:    "1000000000000000000",
			EffectiveAt:           "1",
			SpotPriceMicros:       "200000000",
			QuoterGas:             "100000",
			ExitAmountOutRaw:      "24900000",
			ExitQuoterGas:         "100000",
			BlockTimestamp:        uint64(now.Add(-time.Second).Unix()),
			QuoterCodeHash:        quoterCodeHash,
			PoolManagerCodeHash:   poolManagerCodeHash,
			SettlementCodeHash:    settlementCodeHash,
			StockCodeHash:         stockCodeHash,
		},
	}
}

func validProduct(now time.Time) ProductAccount {
	return ProductAccount{
		ExecutionAccountID: "account-canary-1",
		AgentID:            "agent-canary-001",
		Lifecycle:          "running",
		AccountStatus:      "ready",
		StrategyVersion:    schedulerStrategyVersion(),
		StrategyManifest:   schedulerManifest(),
		LighterAccount:     7,
		LighterAPIKey:      4,
		RobinhoodOwner:     "0x1111111111111111111111111111111111111111",
		RobinhoodVault:     "0x2222222222222222222222222222222222222222",
		RobinhoodSigner:    "0x3333333333333333333333333333333333333333",
		BindingSHA256:      strings.Repeat("4", 64),
		RegistrationStatus: "registered",
		LighterLinked:      true,
		LighterFunded:      true,
		RobinhoodDeployed:  true,
		RobinhoodFunded:    true,
		UserGasReady:       true,
		ExecutionGasReady:  true,
		PolicyActive:       true,
		Reconciled:         true,
		ObservedAt:         now.Add(-time.Second),
		ValidUntil:         now.Add(30 * time.Second),
	}
}

func validExit(now time.Time) PaperExit {
	candidate := validCandidate(now)
	return PaperExit{
		EvaluationID:  "44444444-4444-4444-8444-444444444444",
		EventID:       "55555555-5555-4555-8555-555555555555",
		EpisodeID:     candidate.EpisodeID,
		SourceSession: candidate.SourceSession,
		SourceEventID: candidate.SourceEventID,
		Symbol:        Symbol,
		Status:        "declined",
		Reason:        "net_edge_below_threshold",
		BlockNumber:   candidate.BlockNumber,
		BlockHash:     candidate.BlockHash,
		Evidence:      candidate.Evidence,
		EvaluatedAt:   candidate.EvaluatedAt,
		ClosedAt:      candidate.EvaluatedAt,
	}
}

func TestPaperCandidateValidationFailsClosed(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	candidate := validCandidate(now)
	if err := candidate.Validate(now, 2_000); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*PaperCandidate){
		"unreviewed code": func(value *PaperCandidate) { value.Evidence.StockCodeHash = "0x" + strings.Repeat("f", 64) },
		"stale":           func(value *PaperCandidate) { value.EvaluatedAt = now.Add(-6 * time.Second) },
		"wrong event":     func(value *PaperCandidate) { value.Evidence.TickerSourceEventID = "other" },
		"missing exit":    func(value *PaperCandidate) { value.Evidence.ExitAmountOutRaw = "0" },
		"low edge":        func(value *PaperCandidate) { value.NetEdgePPM = 1_999 },
	} {
		t.Run(name, func(t *testing.T) {
			value := candidate
			mutate(&value)
			if err := value.Validate(now, 2_000); err == nil {
				t.Fatal("invalid candidate was accepted")
			}
		})
	}
}

func TestPaperExitValidationBindsFreshClosureEvidence(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	exit := validExit(now)
	if err := exit.Validate(now); err != nil {
		t.Fatal(err)
	}
	exit.ClosedAt = exit.ClosedAt.Add(time.Millisecond)
	if err := exit.Validate(now); err == nil {
		t.Fatal("mismatched closure time was accepted")
	}
}

func TestPaperEvidenceRejectsUnknownFields(t *testing.T) {
	encoded, err := json.Marshal(validCandidate(time.Now().UTC()).Evidence)
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded[:len(encoded)-1], []byte(`,"unexpected":true}`)...)
	if _, err := DecodePaperEvidence(encoded); err == nil {
		t.Fatal("unknown field was accepted")
	}
}

func TestManifestHashesAreDomainSeparatedAndDeterministic(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	candidate := validCandidate(now)
	dataset, err := DatasetManifest(candidate)
	if err != nil {
		t.Fatal(err)
	}
	market := MarketConfig{
		Symbol: Symbol, SpotToken: stockToken, LighterMarketIndex: 101,
		SpotDecimals: 18, PerpBaseDecimals: 8, PerpPriceDecimals: 6,
		SpotConfigVersion: 1, UIMultiplierE18: "1000000000000000000",
		MaxPriceDeviationBPS: 100, MaxSpotSlippageBPS: 100,
		MaxUnwindPriceDeviationBPS: 2_000, ReviewRecordSHA256: strings.Repeat("5", 64),
		ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour),
	}
	manifest, err := MarketManifest(market)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := SourceEvaluationID(candidate, dataset, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if dataset == manifest || manifest == evaluation || dataset == evaluation {
		t.Fatal("domain-separated hashes collided")
	}
	market.SpotConfigVersion++
	changed, err := MarketManifest(market)
	if err != nil {
		t.Fatal(err)
	}
	if changed == manifest {
		t.Fatal("market policy change did not change the manifest")
	}
}

func TestEstimatedCostUsesCeiling(t *testing.T) {
	candidate := PaperCandidate{GrossEdgePPM: 2, NetEdgePPM: 1}
	cost, err := EstimatedCostMicros(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if cost != 25 {
		t.Fatalf("cost = %d, want 25", cost)
	}
}
