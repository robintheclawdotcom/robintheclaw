package strategyrunner

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestCoordinatorClientDoesNotRetryAmbiguousSend(t *testing.T) {
	intent := fixtureIntent(t)
	key := bytes.Repeat([]byte{3}, 32)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	client := coordinatorClient(t, server.URL, key)
	client.client.Timeout = 25 * time.Millisecond
	_, err := client.SubmitIntent(context.Background(), intent)
	if !errors.Is(err, ErrCoordinatorAmbiguous) || requests.Load() != 1 {
		t.Fatalf("ambiguous send was retried or misclassified: %v, requests=%d", err, requests.Load())
	}
}

func TestCoordinatorClientTreatsConflictAsAmbiguous(t *testing.T) {
	intent := fixtureIntent(t)
	key := bytes.Repeat([]byte{3}, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"database conflict"}`))
	}))
	defer server.Close()
	_, err := coordinatorClient(t, server.URL, key).SubmitIntent(context.Background(), intent)
	if !errors.Is(err, ErrCoordinatorAmbiguous) {
		t.Fatalf("coordinator conflict was not ambiguous: %v", err)
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
