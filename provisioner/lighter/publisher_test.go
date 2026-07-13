package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublisherBridgeReturnsPublicAccountStateOnly(t *testing.T) {
	server, lighter := newTestServer()
	activatePublisherCredential(t, server, lighter)
	body := fmt.Sprintf(`{"executionAccountId":%q}`, testExecutionID)
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, signedPublisherRequest(t, server, "/v1/publisher/account-state", body, strings.Repeat("o", 32)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-RTC-Response-Signature") == "" {
		t.Fatal("publisher response was not authenticated")
	}
	lower := strings.ToLower(response.Body.String())
	for _, forbidden := range []string{"token", "cipher", "kms", "private", "secret", "publickey"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("publisher response exposed %q: %s", forbidden, response.Body.String())
		}
	}
	for _, required := range []string{`"executionAccountId"`, `"accountIndex":42`, `"apiKeyIndex":3`, `"expectedNonce":8`} {
		if !strings.Contains(response.Body.String(), required) {
			t.Fatalf("publisher response missing %s: %s", required, response.Body.String())
		}
	}
}

func TestPublisherBridgeRejectsCredentialSubstitution(t *testing.T) {
	server, lighter := newTestServer()
	activatePublisherCredential(t, server, lighter)
	store := server.store.(*memoryStore)
	store.mu.Lock()
	bound := store.bindings[testExecutionID]
	record := store.records[bound.ActiveCredentialID]
	record.ExecutionAccountID = "22222222-2222-4222-8222-222222222222"
	store.records[bound.ActiveCredentialID] = record
	store.mu.Unlock()
	body := fmt.Sprintf(`{"executionAccountId":%q}`, testExecutionID)
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, signedPublisherRequest(t, server, "/v1/publisher/account-state", body, strings.Repeat("x", 32)))
	if response.Code == http.StatusOK {
		t.Fatalf("substituted credential was accepted: %s", response.Body.String())
	}
}

func TestPublisherBridgeRejectsUpstreamAccountSubstitution(t *testing.T) {
	server, lighter := newTestServer()
	activatePublisherCredential(t, server, lighter)
	lighter.observedAccountIndex = 43
	body := fmt.Sprintf(`{"executionAccountId":%q}`, testExecutionID)
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, signedPublisherRequest(t, server, "/v1/publisher/account-state", body, strings.Repeat("y", 32)))
	if response.Code == http.StatusOK {
		t.Fatalf("substituted account observation was accepted: %s", response.Body.String())
	}
	if _, err := server.store.Active(context.Background(), testExecutionID); err == nil {
		t.Fatal("credential remained active after upstream identity substitution")
	}
}

func TestPublisherBridgeRejectsAdditionalBindingFields(t *testing.T) {
	server, lighter := newTestServer()
	activatePublisherCredential(t, server, lighter)
	body := fmt.Sprintf(`{"executionAccountId":%q,"accountIndex":42}`, testExecutionID)
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, signedPublisherRequest(t, server, "/v1/publisher/account-state", body, strings.Repeat("z", 32)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPublisherBridgePropagatesLighterRateLimit(t *testing.T) {
	server, lighter := newTestServer()
	activatePublisherCredential(t, server, lighter)
	lighter.observeErr = errObservationRateLimited
	body := fmt.Sprintf(`{"executionAccountId":%q}`, testExecutionID)
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, signedPublisherRequest(t, server, "/v1/publisher/account-state", body, strings.Repeat("r", 32)))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func activatePublisherCredential(t *testing.T, server *server, lighter *fakeLighter) {
	t.Helper()
	link, err := server.service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		AccountIndex:       42,
		APIKeyIndex:        3,
		Nonce:              7,
	})
	if err != nil {
		t.Fatal(err)
	}
	lighter.registered = link.PublicKey
	if _, linked, err := server.service.confirm(context.Background(), confirmRequest{
		ExecutionAccountID: testExecutionID,
		LinkID:             link.LinkID,
		L1Signature:        validTestSignature(),
	}); err != nil || !linked {
		t.Fatalf("activate credential: linked=%v err=%v", linked, err)
	}
}
