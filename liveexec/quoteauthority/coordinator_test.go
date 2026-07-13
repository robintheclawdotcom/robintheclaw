package quoteauthority

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

func TestCoordinatorPublisherRetriesExactQuoteAfterCommitTimeout(t *testing.T) {
	quote := publicationFixture()
	encoded, _ := json.Marshal(quote)
	digest := sha256.Sum256(encoded)
	payloadSHA256 := hex.EncodeToString(digest[:])
	key := bytes.Repeat([]byte{5}, 32)
	var submissions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/market-quotes" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		attempt := submissions.Add(1)
		if attempt == 1 {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.MarketQuoteReceipt{
			Status: "duplicate", SourceSession: quote.SourceSession, SourceEventID: quote.SourceEventID,
			PayloadSHA256: payloadSHA256,
		})
	}))
	defer server.Close()
	publisher, err := NewCoordinatorPublisher(server.URL, "quote-publisher", key)
	if err != nil {
		t.Fatal(err)
	}
	publisher.client.Timeout = 25 * time.Millisecond
	publisher.now = func() time.Time { return time.Unix(100, 0) }
	publisher.nonce = func() (string, error) { return "0123456789abcdef0123456789abcdef", nil }
	receipt, err := publisher.Publish(context.Background(), quote)
	if err != nil || receipt.Status != "duplicate" || receipt.PayloadSHA256 != payloadSHA256 || submissions.Load() != 2 {
		t.Fatalf("exact quote retry failed: receipt=%+v err=%v submissions=%d", receipt, err, submissions.Load())
	}
}

func publicationFixture() protocol.MarketQuotePublication {
	return protocol.MarketQuotePublication{
		Source: "execution-authority", SourceSession: "authority-session-1", SourceEventID: "authority-event-1",
		SourceSequence: 1, ExecutionAccountID: "account-canary-1", MarketManifest: testHash("market"),
		StrategyManifestSHA256: protocol.StrategyManifestSHA256, RouteSHA256: protocol.RouteSHA256,
		LighterMarketIndex: 101, QuoteBlockHash: testHash("block"), MarkPrice: 25_000, PublisherAtMS: 99_000,
		ReceivedAtMS: 99_500, ExpiresAtMS: 102_000,
		IntentID:           "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SpotUnwindAmountIn: "2000000", SpotUnwindExpectedAmountOut: "25000000",
		SubmissionDeadlineMS: 102_000, ReconciliationDeadlineMS: 86_502_000,
	}
}

func TestCoordinatorPublisherRejectsPayloadCollision(t *testing.T) {
	key := bytes.Repeat([]byte{5}, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()
	publisher, err := NewCoordinatorPublisher(server.URL, "quote-publisher", key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = publisher.Publish(context.Background(), publicationFixture())
	if err != ErrMarketQuoteConflict {
		t.Fatalf("payload collision was not rejected: %v", err)
	}
}
