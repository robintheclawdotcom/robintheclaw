package quoteauthority

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

func TestServerAuthenticatesRequestsAndRejectsReplay(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	service, _ := NewService(&fakeAdapter{result: adapterResult(protocol.ActionEntry)}, &fakePublisher{}, privateKey, 101)
	service.now = func() time.Time { return time.UnixMilli(100_000) }
	key := []byte("01234567890123456789012345678901")
	auth, _ := protocol.NewAuthenticator(key, "strategy-runner")
	server := NewServer(service, auth, true).Handler()
	body, _ := json.Marshal(protocol.QuoteRequest{
		RequestID: testHash("request"), ExecutionAccountID: "account-canary-1",
		SourceEvaluationID: testHash("evaluation"), MarketManifest: testHash("market"), Action: protocol.ActionEntry, RequestedAtMS: 99_500,
	})
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	signature := hex.EncodeToString(protocol.RequestMAC(key, http.MethodPost, "/v1/executable-quotes", "strategy-runner", timestamp, "nonce-1", body))
	request := httptest.NewRequest(http.MethodPost, "/v1/executable-quotes", bytes.NewReader(body))
	setAuthHeaders(request, timestamp, "nonce-1", signature)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated quote failed: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/executable-quotes", bytes.NewReader(body))
	setAuthHeaders(request, timestamp, "nonce-1", signature)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("replayed quote returned %d", response.Code)
	}
}

func TestDisabledAuthorityDoesNotQuote(t *testing.T) {
	response := httptest.NewRecorder()
	NewServer(nil, nil, false).Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/executable-quotes", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled authority returned %d", response.Code)
	}
}

func setAuthHeaders(request *http.Request, timestamp, nonce, signature string) {
	request.Header.Set("X-Robin-Caller", "strategy-runner")
	request.Header.Set("X-Robin-Timestamp", timestamp)
	request.Header.Set("X-Robin-Nonce", nonce)
	request.Header.Set("X-Robin-Signature", signature)
}
