package publisher

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLighterBridgeRequestContainsOnlyExecutionAccountID(t *testing.T) {
	client, binding, closeServer := lighterBridgeFixture(t, func(request map[string]any, response *lighterBridgeResponse) {
		if len(request) != 1 || request["executionAccountId"] != "10000000-0000-4000-8000-000000000001" {
			t.Fatalf("unexpected bridge request: %#v", request)
		}
	})
	defer closeServer()
	observation, err := client.Collect(context.Background(), "10000000-0000-4000-8000-000000000001", binding)
	if err != nil {
		t.Fatal(err)
	}
	if observation.AccountIndex != 77 || observation.Nonce != 42 || observation.ExpectedNonce != 42 ||
		!observation.CollateralReady || !observation.RESTReconstructed {
		t.Fatalf("unexpected observation: %+v", observation)
	}
}

func TestLighterBridgeRejectsCredentialAccountSubstitution(t *testing.T) {
	client, binding, closeServer := lighterBridgeFixture(t, func(_ map[string]any, response *lighterBridgeResponse) {
		response.AccountIndex = 78
	})
	defer closeServer()
	_, err := client.Collect(context.Background(), "10000000-0000-4000-8000-000000000001", binding)
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("expected identity mismatch, got %v", err)
	}
}

func lighterBridgeFixture(t *testing.T, mutate func(map[string]any, *lighterBridgeResponse)) (*LighterClient, LighterBinding, func()) {
	t.Helper()
	key := strings.Repeat("42", 32)
	caller := "account-publisher"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		var input map[string]any
		if err := json.Unmarshal(body, &input); err != nil {
			t.Fatal(err)
		}
		response := lighterBridgeResponse{
			ExecutionAccountID: "10000000-0000-4000-8000-000000000001", AccountIndex: 77, APIKeyIndex: 4,
			MarketID: 5, CredentialVersion: 1, Nonce: 42, ExpectedNonce: 42, CollateralRaw: "100",
			MaintenanceRequirementRaw: "25", MaintenanceMarginRatioMicros: 4_000_000,
			NoUnknownOrders: true, NoUnknownPositions: true, Flat: true, RESTReconstructed: true,
			StateDigest: strings.Repeat("a", 64), ObservedAt: time.Now().UTC(),
		}
		mutate(input, &response)
		encoded, _ := json.Marshal(response)
		digest := sha256.Sum256(encoded)
		canonical := fmt.Sprintf("RESPONSE\n%s\n%s\n%s\n%d\n%x", request.URL.Path, caller, request.Header.Get("X-RTC-Nonce"), http.StatusOK, digest)
		decodedKey, _ := hex.DecodeString(key)
		mac := hmac.New(sha256.New, decodedKey)
		_, _ = mac.Write([]byte(canonical))
		writer.Header().Set("X-RTC-Response-Signature", hex.EncodeToString(mac.Sum(nil)))
		_, _ = writer.Write(encoded)
	}))
	client, err := NewLighterClient(EndpointConfig{URL: server.URL, Caller: caller, HMACKey: key}, server.Client())
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return client, LighterBinding{AccountIndex: 77, APIKeyIndex: 4, MarketID: 5, MinimumCollateralRaw: "50"}, server.Close
}
