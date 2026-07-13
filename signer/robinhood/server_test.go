package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDisabledServerFailsClosed(t *testing.T) {
	server := &Server{config: Config{Enabled: false}}
	handler := server.Handler()

	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected readiness status: %d", ready.Code)
	}

	execute := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/spot-intents", strings.NewReader("{}"))
	handler.ServeHTTP(execute, request)
	if execute.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected execution status: %d", execute.Code)
	}
}

func TestUnknownJSONFieldsAreRejected(t *testing.T) {
	journal := &fakeJournal{ready: true}
	writer := &Writer{journal: journal}
	writer.ready.Store(true)
	config := Config{
		Enabled:               true,
		APIHMACKey:            []byte(strings.Repeat("a", 32)),
		CallerID:              "execution-coordinator",
		MaxRequestsPerMinute:  60,
		MaxConcurrentRequests: 4,
	}
	server := &Server{
		config: config,
		writer: writer,
	}
	response := httptest.NewRecorder()
	body := []byte(`{"request_id":"x","target":"0x01"}`)
	request := authenticatedRequest(config, body, strings.Repeat("n", 32))
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field was not rejected: %d", response.Code)
	}
}

func TestAuthorizationNonceCannotBeReplayed(t *testing.T) {
	journal := &fakeJournal{ready: true}
	writer := &Writer{journal: journal}
	writer.ready.Store(true)
	config := Config{
		Enabled:               true,
		APIHMACKey:            []byte(strings.Repeat("a", 32)),
		CallerID:              "execution-coordinator",
		MaxRequestsPerMinute:  60,
		MaxConcurrentRequests: 4,
	}
	server := &Server{config: config, writer: writer}
	body := []byte(`{"request_id":"x","target":"0x01"}`)
	nonce := strings.Repeat("r", 32)
	first := httptest.NewRecorder()
	server.Handler().ServeHTTP(first, authenticatedRequest(config, body, nonce))
	if first.Code != http.StatusBadRequest {
		t.Fatalf("unexpected first status: %d", first.Code)
	}
	second := httptest.NewRecorder()
	server.Handler().ServeHTTP(second, authenticatedRequest(config, body, nonce))
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("replayed nonce was accepted: %d", second.Code)
	}
}

func authenticatedRequest(config Config, body []byte, nonce string) *http.Request {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	request := httptest.NewRequest(http.MethodPost, "/v1/spot-intents", strings.NewReader(string(body)))
	request.Header.Set("X-RTC-Caller", config.CallerID)
	request.Header.Set("X-RTC-Timestamp", timestamp)
	request.Header.Set("X-RTC-Nonce", nonce)
	digest := sha256.Sum256(body)
	canonical := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%s\n%x",
		request.Method,
		request.URL.Path,
		config.CallerID,
		timestamp,
		nonce,
		digest,
	)
	mac := hmac.New(sha256.New, config.APIHMACKey)
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set("X-RTC-Signature", hex.EncodeToString(mac.Sum(nil)))
	return request
}
