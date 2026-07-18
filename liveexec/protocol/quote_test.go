package protocol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestQuoteBundleSignatureBindsEveryField(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	quote := testQuote()
	if err := quote.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	if err := quote.Verify(publicKey, 101, 100_000); err != nil {
		t.Fatalf("valid quote rejected: %v", err)
	}

	tampered := quote
	tampered.ExecutionAccountID = "account-canary-2"
	if err := tampered.Verify(publicKey, 101, 100_000); err == nil {
		t.Fatal("cross-account substitution accepted")
	}
	tampered = quote
	tampered.Spot.MinimumAmountOut = "1"
	if err := tampered.Verify(publicKey, 101, 100_000); err == nil {
		t.Fatal("amount substitution accepted")
	}
	otherPublicKey, _, _ := ed25519.GenerateKey(nil)
	if err := quote.Verify(otherPublicKey, 101, 100_000); err == nil {
		t.Fatal("untrusted quote key accepted")
	}
}

func TestQuoteBundleRejectsStaleAndMismatchedPolicy(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	quote := testQuote()
	if err := quote.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	if err := quote.Verify(publicKey, 101, quote.ExpiresAtMS); err == nil {
		t.Fatal("expired quote accepted")
	}

	quote = testQuote()
	quote.StrategyVersion = "user-strategy"
	if err := quote.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	if err := quote.Verify(publicKey, 101, 100_000); err == nil {
		t.Fatal("unapproved strategy accepted")
	}

	quote = testQuote()
	quote.SchemaVersion = 3
	if err := quote.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	if err := quote.Verify(publicKey, 101, 100_000); err == nil {
		t.Fatal("quote schema v3 accepted")
	}
}

func TestExitQuoteRejectsSubstitutedPersistenceAuthority(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	quote := testUnwindQuote(StrategyManifestSHA256)
	if err := quote.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	if err := quote.Verify(publicKey, 101, 100_000); err != nil {
		t.Fatal(err)
	}
	quote.ExitAuthority.ExecutionAccountID = "account-canary-2"
	if err := quote.Verify(publicKey, 101, 100_000); err == nil {
		t.Fatal("cross-account exit authority substitution accepted")
	}
}

func TestUnwindTargetManifestAllowlist(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	for _, target := range []string{
		StrategyManifestSHA256,
		PreviousStrategyManifestSHA256,
		LegacyStrategyManifestSHA256,
	} {
		quote := testUnwindQuote(target)
		if err := quote.Sign(privateKey); err != nil {
			t.Fatal(err)
		}
		if err := quote.Verify(publicKey, 101, 100_000); err != nil {
			t.Fatalf("allowed unwind target %s was rejected: %v", target, err)
		}
	}
}

func TestSpotOnlyQuoteRejectsNonzeroPerpBase(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	quote := testUnwindQuote(StrategyManifestSHA256)
	quote.Perp.Phase = "spot_only"
	quote.Perp.BaseAmount = 1
	if err := quote.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	if err := quote.Verify(publicKey, 101, 100_000); err == nil {
		t.Fatal("spot-only quote accepted a nonzero perp base")
	}
}

func TestUnwindTargetManifestIsMandatoryAndSigned(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	for _, target := range []string{"", hashValue("unknown-target")[2:]} {
		quote := testUnwindQuote(target)
		if err := quote.Sign(privateKey); err != nil {
			t.Fatal(err)
		}
		if err := quote.Verify(publicKey, 101, 100_000); err == nil {
			t.Fatalf("invalid unwind target %q was accepted", target)
		}
	}

	quote := testUnwindQuote(PreviousStrategyManifestSHA256)
	if err := quote.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	quote.TargetStrategyManifestSHA256 = StrategyManifestSHA256
	if err := quote.Verify(publicKey, 101, 100_000); err == nil {
		t.Fatal("post-signature unwind target substitution was accepted")
	}
}

func TestEntryRejectsTargetManifest(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	quote := testQuote()
	quote.TargetStrategyManifestSHA256 = PreviousStrategyManifestSHA256
	if err := quote.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	if err := quote.Verify(publicKey, 101, 100_000); err == nil {
		t.Fatal("entry quote accepted an unwind target manifest")
	}
}

func TestQuoteBundleRejectsUnreviewedMarketIndex(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	quote := testQuote()
	quote.Perp.MarketIndex = 102
	if err := quote.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	if err := quote.Verify(publicKey, 101, 100_000); err == nil {
		t.Fatal("signed quote for an unreviewed market index accepted")
	}
}

func TestAuthenticatorRejectsReplayAndTampering(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	auth, err := NewAuthenticator(key, "strategy-runner")
	if err != nil {
		t.Fatal(err)
	}
	auth.now = func() time.Time { return time.UnixMilli(100_000) }
	body := []byte(`{"request":"fixed"}`)
	timestamp := "100000"
	signature := hex.EncodeToString(RequestMAC(key, "POST", "/v1/run", "strategy-runner", timestamp, "nonce-1", body))
	if err := auth.Verify("POST", "/v1/run", "strategy-runner", timestamp, "nonce-1", signature, body); err != nil {
		t.Fatalf("valid authentication rejected: %v", err)
	}
	if err := auth.Verify("POST", "/v1/run", "strategy-runner", timestamp, "nonce-1", signature, body); err == nil {
		t.Fatal("nonce replay accepted")
	}
	if err := auth.Verify("POST", "/v1/run", "strategy-runner", timestamp, "nonce-2", signature, append(body, 'x')); err == nil {
		t.Fatal("tampered body accepted")
	}
}

func TestResponseMACBindsRequestAndResponse(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	body := []byte(`{"status":"persisted"}`)
	signature := hex.EncodeToString(ResponseMAC(key, "/v1/run", "strategy-runner", "nonce-1", 200, body))
	if err := VerifyResponseMAC(key, "/v1/run", "strategy-runner", "nonce-1", 200, body, signature); err != nil {
		t.Fatalf("valid response authentication rejected: %v", err)
	}
	for name, verify := range map[string]func() error{
		"path": func() error {
			return VerifyResponseMAC(key, "/v1/other", "strategy-runner", "nonce-1", 200, body, signature)
		},
		"caller": func() error {
			return VerifyResponseMAC(key, "/v1/run", "other-runner", "nonce-1", 200, body, signature)
		},
		"nonce": func() error {
			return VerifyResponseMAC(key, "/v1/run", "strategy-runner", "nonce-2", 200, body, signature)
		},
		"status": func() error {
			return VerifyResponseMAC(key, "/v1/run", "strategy-runner", "nonce-1", 201, body, signature)
		},
		"body": func() error {
			return VerifyResponseMAC(key, "/v1/run", "strategy-runner", "nonce-1", 200, []byte(`{}`), signature)
		},
		"format": func() error {
			return VerifyResponseMAC(key, "/v1/run", "strategy-runner", "nonce-1", 200, body, "not-hex")
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := verify(); err == nil {
				t.Fatal("substituted response accepted")
			}
		})
	}
}

func testUnwindQuote(target string) QuoteBundle {
	quote := testQuote()
	quote.TargetStrategyManifestSHA256 = target
	quote.Action = ActionUnwind
	quote.Spot.Side = "sell"
	quote.Spot.MinimumAmountOut = "24000000"
	quote.Perp.Side = "long"
	quote.Perp.ReduceOnly = true
	quote.Perp.Phase = "perp_and_spot"
	quote.ExitAuthority = &ExitQuoteAuthority{
		Source: "execution-authority", SourceSession: "authority-session-1", SourceEventID: "authority-event-1",
		SourceSequence: 1, ExecutionAccountID: quote.ExecutionAccountID, IntentID: hashValue("intent"),
		MarketManifest: quote.MarketManifest, PayloadSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ReceivedAtMS: 99_500, SubmissionDeadlineMS: quote.ExpiresAtMS,
		ReconciliationDeadlineMS: quote.ExpiresAtMS + MaximumExitReconciliationMS,
	}
	return quote
}

func testQuote() QuoteBundle {
	return QuoteBundle{
		SchemaVersion:          QuoteSchemaVersion,
		RequestID:              hashValue("request"),
		ExecutionAccountID:     "account-canary-1",
		SourceEvaluationID:     hashValue("evaluation"),
		MarketManifest:         hashValue("market"),
		StrategyVersion:        StrategyVersion,
		StrategyManifestSHA256: StrategyManifestSHA256,
		SourceConfigSHA256:     SourceConfigSHA256,
		RouteSHA256:            RouteSHA256,
		OraclePolicySHA256:     OraclePolicySHA256,
		RiskPolicySHA256:       RiskPolicySHA256,
		Action:                 ActionEntry,
		Source: SourceIdentity{
			AdapterID:   "reviewed-adapter-v1",
			SpotSource:  "robinhood-rpc-quoter",
			PerpSource:  "lighter-orderbook",
			OracleRound: "101",
		},
		Spot: SpotQuote{
			Venue:                SpotVenue,
			ChainID:              ChainID,
			SettlementToken:      SettlementToken,
			StockToken:           StockToken,
			Router:               Router,
			Side:                 "buy",
			SettlementAmount:     "25000000",
			StockAmount:          "2000000",
			MinimumAmountOut:     "1990000",
			ExpectedUIMultiplier: "500000000000000000",
			MinOracleRoundID:     "101",
			ReferencePriceMicros: 25_000_000,
			BlockHash:            hashValue("block"),
			ObservedAtMS:         99_000,
		},
		Perp: PerpQuote{
			Venue:         PerpVenue,
			Symbol:        Symbol,
			MarketIndex:   101,
			Side:          "short",
			BaseAmount:    1_000_000,
			BaseDecimals:  6,
			PriceDecimals: 3,
			LimitPrice:    25_000,
			MarkPrice:     25_000,
			ObservedAtMS:  99_000,
		},
		ObservedAtMS: 99_000,
		ExpiresAtMS:  102_000,
	}
}

func hashValue(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "0x" + hex.EncodeToString(digest[:])
}
