package strategyrunner

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

func TestServerRequiresAuthenticationAndRejectsReplay(t *testing.T) {
	service, input := validInput(t, protocol.ActionEntry)
	key := []byte("01234567890123456789012345678901")
	auth, err := protocol.NewAuthenticator(key, "evaluation-service")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(service, auth, true).Handler()
	body, _ := json.Marshal(input)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	signature := hex.EncodeToString(protocol.RequestMAC(key, "POST", "/v1/run", "evaluation-service", timestamp, "nonce-1", body))
	request := authenticatedRequest(body, timestamp, "nonce-1", signature)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated request failed: %d %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	server.ServeHTTP(response, authenticatedRequest(body, timestamp, "nonce-1", signature))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("replayed request returned %d", response.Code)
	}

	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/run", bytes.NewReader(body)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request returned %d", response.Code)
	}
}

func TestServerRejectsUnknownStrategyParameters(t *testing.T) {
	_, input := validInput(t, protocol.ActionEntry)
	key := []byte("01234567890123456789012345678901")
	auth, _ := protocol.NewAuthenticator(key, "evaluation-service")
	service, _ := validInput(t, protocol.ActionEntry)
	server := NewServer(service, auth, true).Handler()
	body, _ := json.Marshal(input)
	body = append(bytes.TrimSuffix(body, []byte("}")), []byte(`,"market":"TSLA"}`)...)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	signature := hex.EncodeToString(protocol.RequestMAC(key, "POST", "/v1/run", "evaluation-service", timestamp, "nonce-2", body))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, authenticatedRequest(body, timestamp, "nonce-2", signature))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown strategy parameter returned %d", response.Code)
	}
}

func TestDisabledServerStaysFailClosed(t *testing.T) {
	server := NewServer(nil, nil, false).Handler()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/run", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled runner returned %d", response.Code)
	}
}

func authenticatedRequest(body []byte, timestamp, nonce, signature string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/run", bytes.NewReader(body))
	request.Header.Set("X-Robin-Caller", "evaluation-service")
	request.Header.Set("X-Robin-Timestamp", timestamp)
	request.Header.Set("X-Robin-Nonce", nonce)
	request.Header.Set("X-Robin-Signature", signature)
	return request
}
