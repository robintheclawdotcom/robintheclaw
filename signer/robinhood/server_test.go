package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
		ExecutionAccountID:    "account-canary-1",
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
	body := []byte(`{"execution_account_id":"account-canary-1","request_id":"x","target":"0x01"}`)
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
		ExecutionAccountID:    "account-canary-1",
		APIHMACKey:            []byte(strings.Repeat("a", 32)),
		CallerID:              "execution-coordinator",
		MaxRequestsPerMinute:  60,
		MaxConcurrentRequests: 4,
	}
	server := &Server{config: config, writer: writer}
	body := []byte(`{"execution_account_id":"account-canary-1","request_id":"x","target":"0x01"}`)
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

func TestServerReturnsJournaledOutcomeAfterBroadcastTimeout(t *testing.T) {
	fixture := newWriterFixture(t)
	fixture.chain.sendError = context.DeadlineExceeded
	fixture.config.APIHMACKey = []byte(strings.Repeat("a", 32))
	fixture.config.CallerID = "execution-coordinator"
	fixture.config.MaxRequestsPerMinute = 60
	fixture.config.MaxConcurrentRequests = 4
	fixture.config.RequestTimeout = time.Second
	server := &Server{config: fixture.config, writer: fixture.writer}
	body, err := json.Marshal(validRequest())
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authenticatedRequest(fixture.config, body, strings.Repeat("t", 32)))
	if response.Code != http.StatusAccepted {
		t.Fatalf("ambiguous submission returned %d: %s", response.Code, response.Body.String())
	}
	var submission Submission
	if err := json.NewDecoder(response.Body).Decode(&submission); err != nil {
		t.Fatal(err)
	}
	if submission.Status != "ambiguous" || submission.RequestID != validRequest().RequestID {
		t.Fatalf("unexpected response: %#v", submission)
	}

	retryResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(
		retryResponse,
		authenticatedRequest(fixture.config, body, strings.Repeat("u", 32)),
	)
	if retryResponse.Code != http.StatusAccepted {
		t.Fatalf("journaled retry returned %d: %s", retryResponse.Code, retryResponse.Body.String())
	}
	var retry Submission
	if err := json.NewDecoder(retryResponse.Body).Decode(&retry); err != nil {
		t.Fatal(err)
	}
	if retry != submission {
		t.Fatalf("journaled retry changed the response: %#v", retry)
	}
	if len(fixture.chain.sent) != 1 {
		t.Fatalf("journaled retry rebroadcast the transaction: %d", len(fixture.chain.sent))
	}
}

func TestServerDistinguishesPreflightRejection(t *testing.T) {
	fixture := newWriterFixture(t)
	fixture.chain.simulationError = errors.New("execution reverted")
	fixture.config.APIHMACKey = []byte(strings.Repeat("a", 32))
	fixture.config.CallerID = "execution-coordinator"
	fixture.config.MaxRequestsPerMinute = 60
	fixture.config.MaxConcurrentRequests = 4
	fixture.config.RequestTimeout = time.Second
	server := &Server{config: fixture.config, writer: fixture.writer}
	body, err := json.Marshal(validRequest())
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authenticatedRequest(fixture.config, body, strings.Repeat("p", 32)))
	if response.Code != http.StatusConflict {
		t.Fatalf("preflight rejection returned %d: %s", response.Code, response.Body.String())
	}
	if fixture.journal.reservation != nil || len(fixture.chain.sent) != 0 {
		t.Fatal("preflight rejection crossed the journal boundary")
	}
}

func TestUnknownExecutionAccountIsRejectedBeforeJournalUse(t *testing.T) {
	fixture := newWriterFixture(t)
	fixture.config.APIHMACKey = []byte(strings.Repeat("a", 32))
	fixture.config.CallerID = "execution-coordinator"
	fixture.config.MaxRequestsPerMinute = 60
	fixture.config.MaxConcurrentRequests = 4
	server := &Server{config: fixture.config, writer: fixture.writer}
	request := validRequest()
	request.ExecutionAccountID = "account-canary-2"
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(
		response,
		authenticatedRequest(fixture.config, body, strings.Repeat("v", 32)),
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unknown account got status %d", response.Code)
	}
	if len(fixture.chain.sent) != 0 || fixture.journal.reservation != nil {
		t.Fatal("unknown account crossed the signer boundary")
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
