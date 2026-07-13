package strategyrunner

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoordinatorClientPersistsExactIntent(t *testing.T) {
	intent := fixtureIntent(t)
	key := bytes.Repeat([]byte{3}, 32)
	var received []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/intents" || request.Method != http.MethodPost {
			t.Errorf("unexpected coordinator request: %s %s", request.Method, request.URL.Path)
		}
		received, _ = io.ReadAll(request.Body)
		timestamp := request.Header.Get("X-RTC-Timestamp")
		nonce := request.Header.Get("X-RTC-Nonce")
		wantMAC := coordinatorMAC(key, request.URL.Path, "strategy-runner", timestamp, nonce, received)
		gotMAC, err := hex.DecodeString(request.Header.Get("X-RTC-Signature"))
		if err != nil || !hmac.Equal(gotMAC, wantMAC) || request.Header.Get("X-RTC-Caller") != "strategy-runner" {
			t.Error("coordinator request authentication mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(coordinatorSaga{
			IntentID: intent.ID, State: "prechecked", Version: 1, SpotReceivedRaw: "0",
		})
	}))
	defer server.Close()
	client := coordinatorClient(t, server.URL, key)
	persistence, err := client.SubmitIntent(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	wantBody, _ := json.Marshal(intent)
	if !bytes.Equal(received, wantBody) {
		t.Fatal("coordinator did not receive the exact generated PairIntent")
	}
	if persistence.Status != "persisted" || persistence.IntentID != intent.ID || persistence.CoordinatorState != "prechecked" || persistence.CoordinatorVersion != 1 {
		t.Fatal("invalid persistence receipt")
	}
}

func TestCoordinatorClientRejectsUnprovenPersistence(t *testing.T) {
	intent := fixtureIntent(t)
	key := bytes.Repeat([]byte{3}, 32)
	cases := map[string]http.HandlerFunc{
		"wrong intent": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(coordinatorSaga{IntentID: testHash("wrong"), State: "prechecked", Version: 1, SpotReceivedRaw: "0"})
		},
		"wrong state": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(coordinatorSaga{IntentID: intent.ID, State: "created", SpotReceivedRaw: "0"})
		},
		"unknown response field": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"intent_id":"` + intent.ID + `","state":"prechecked","version":1,"perp_filled_base":0,"perp_unwound_base":0,"spot_received_raw":"0","extra":true}`))
		},
		"oversized response": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write(bytes.Repeat([]byte{'x'}, maximumCoordinatorResponseBytes+1))
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			_, err := coordinatorClient(t, server.URL, key).SubmitIntent(context.Background(), intent)
			if !errors.Is(err, ErrCoordinatorAmbiguous) {
				t.Fatalf("unproven persistence did not fail ambiguous: %v", err)
			}
		})
	}
}

func TestCoordinatorClientNeverFollowsRedirects(t *testing.T) {
	intent := fixtureIntent(t)
	key := bytes.Repeat([]byte{3}, 32)
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	_, err := coordinatorClient(t, source.URL, key).SubmitIntent(context.Background(), intent)
	if !errors.Is(err, ErrCoordinatorAmbiguous) || redirected.Load() != 0 {
		t.Fatalf("redirect was followed or misclassified: %v", err)
	}
}

func TestCoordinatorClientReconcilesCommitThenTimeoutWithoutResend(t *testing.T) {
	intent := fixtureIntent(t)
	key := bytes.Repeat([]byte{3}, 32)
	encoded, _ := json.Marshal(intent)
	digest := sha256.Sum256(encoded)
	payloadSHA256 := hex.EncodeToString(digest[:])
	var submissions atomic.Int32
	var statusQueries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/intents":
			submissions.Add(1)
			time.Sleep(100 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
		case "/v1/intent-status":
			statusQueries.Add(1)
			body, _ := io.ReadAll(request.Body)
			var query intentStatusRequest
			if err := json.Unmarshal(body, &query); err != nil || query.IntentID != intent.ID || query.PayloadSHA256 != payloadSHA256 {
				t.Error("status query did not bind the exact payload digest")
			}
			wantMAC := coordinatorMAC(key, request.URL.Path, "strategy-runner", request.Header.Get("X-RTC-Timestamp"), request.Header.Get("X-RTC-Nonce"), body)
			gotMAC, _ := hex.DecodeString(request.Header.Get("X-RTC-Signature"))
			if !hmac.Equal(wantMAC, gotMAC) {
				t.Error("status query authentication mismatch")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(intentStatusResponse{
				IntentID: intent.ID, PayloadSHA256: payloadSHA256, Status: "persisted",
				Saga: &coordinatorSaga{IntentID: intent.ID, State: "perp_submitted", Version: 2, SpotReceivedRaw: "0"},
			})
		default:
			t.Errorf("unexpected path %s", request.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := coordinatorClient(t, server.URL, key)
	client.client.Timeout = 25 * time.Millisecond
	persistence, err := client.SubmitIntent(context.Background(), intent)
	if err != nil || persistence.IntentID != intent.ID || persistence.CoordinatorState != "perp_submitted" ||
		persistence.CoordinatorVersion != 2 || submissions.Load() != 1 || statusQueries.Load() != 1 {
		t.Fatalf("commit timeout was not reconciled: persistence=%+v err=%v submissions=%d status=%d", persistence, err, submissions.Load(), statusQueries.Load())
	}
}

func TestCoordinatorClientRejectsPayloadCollision(t *testing.T) {
	intent := fixtureIntent(t)
	key := bytes.Repeat([]byte{3}, 32)
	encoded, _ := json.Marshal(intent)
	digest := sha256.Sum256(encoded)
	payloadSHA256 := hex.EncodeToString(digest[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/v1/intents" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"intent payload conflict"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(intentStatusResponse{
			IntentID: intent.ID, PayloadSHA256: payloadSHA256, Status: "conflict",
		})
	}))
	defer server.Close()
	_, err := coordinatorClient(t, server.URL, key).SubmitIntent(context.Background(), intent)
	if !errors.Is(err, ErrCoordinatorPayloadConflict) {
		t.Fatalf("coordinator payload collision was not rejected: %v", err)
	}
}

func TestCoordinatorClientReconcilesExitCommitTimeoutWithoutResend(t *testing.T) {
	exit := fixtureExit()
	intentKey := bytes.Repeat([]byte{3}, 32)
	exitKey := bytes.Repeat([]byte{4}, 32)
	encoded, _ := json.Marshal(exit)
	digest := sha256.Sum256(encoded)
	payloadSHA256 := hex.EncodeToString(digest[:])
	var submissions atomic.Int32
	var statusQueries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/exits":
			submissions.Add(1)
			time.Sleep(100 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
		case "/v1/exit-status":
			statusQueries.Add(1)
			body, _ := io.ReadAll(request.Body)
			wantMAC := coordinatorMAC(exitKey, request.URL.Path, "strategy-exit", request.Header.Get("X-RTC-Timestamp"), request.Header.Get("X-RTC-Nonce"), body)
			gotMAC, _ := hex.DecodeString(request.Header.Get("X-RTC-Signature"))
			if !hmac.Equal(wantMAC, gotMAC) {
				t.Error("exit status authentication mismatch")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(exitStatusResponse{
				RequestID: exit.RequestID, PayloadSHA256: payloadSHA256, Status: "persisted",
				Saga: &coordinatorSaga{IntentID: exit.IntentID, State: "unwinding", Version: 7, PerpFilledBase: 1_000_000, SpotReceivedRaw: "2000000"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewCoordinatorClientWithExit(server.URL, "strategy-runner", intentKey, "strategy-exit", exitKey)
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return time.Unix(100, 0) }
	client.nonce = func() (string, error) { return "0123456789abcdef0123456789abcdef", nil }
	client.client.Timeout = 25 * time.Millisecond
	persistence, err := client.SubmitExit(context.Background(), exit)
	if err != nil || persistence.RequestID != exit.RequestID || persistence.CoordinatorState != "unwinding" ||
		submissions.Load() != 1 || statusQueries.Load() != 1 {
		t.Fatalf("exit commit timeout was not reconciled: persistence=%+v err=%v submissions=%d status=%d", persistence, err, submissions.Load(), statusQueries.Load())
	}
}

func TestCoordinatorClientRejectsUnsafeConfig(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	for name, baseURL := range map[string]string{
		"public http": "http://coordinator.example",
		"credentials": "https://user:pass@coordinator.example",
		"path":        "https://coordinator.example/other",
		"query":       "https://coordinator.example?mode=unsafe",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewCoordinatorClient(baseURL, "strategy-runner", key); err == nil {
				t.Fatal("unsafe coordinator URL accepted")
			}
		})
	}
	if _, err := NewCoordinatorClient("https://coordinator.example", "UPPER", key); err == nil {
		t.Fatal("invalid coordinator caller accepted")
	}
	if _, err := NewCoordinatorClient("https://coordinator.example", "strategy-runner", key[:31]); err == nil {
		t.Fatal("invalid coordinator key accepted")
	}
}

func coordinatorClient(t *testing.T, baseURL string, key []byte) *CoordinatorClient {
	t.Helper()
	client, err := NewCoordinatorClient(baseURL, "strategy-runner", key)
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return time.Unix(100, 0) }
	client.nonce = func() (string, error) { return "0123456789abcdef0123456789abcdef", nil }
	return client
}

func fixtureIntent(t *testing.T) PairIntent {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join("..", "testdata", "pair-intent-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	var intent PairIntent
	if err := json.Unmarshal(encoded, &intent); err != nil {
		t.Fatal(err)
	}
	return intent
}

func fixtureExit() ExitSubmission {
	return ExitSubmission{
		RequestID:                  "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ExecutionAccountID:         "account-canary-1",
		IntentID:                   "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		QuoteSourceSession:         "authority-session-1",
		QuoteSourceEventID:         "authority-event-1",
		QuotePayloadSHA256:         strings.Repeat("c", 64),
		PerpUnwindPrice:            25_000,
		MinimumUnwindSettlementOut: "24000000",
		RequestedAtMS:              99_500,
		SubmissionDeadlineMS:       102_000,
		ReconciliationDeadlineMS:   86_502_000,
		Reason:                     "strategy_exit",
	}
}
