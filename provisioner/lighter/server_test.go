package main

import (
	"bytes"
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

func newTestServer() (*server, *fakeLighter) {
	service, store, lighter := newTestService()
	return &server{
		config: config{
			Enabled:                       true,
			CallerID:                      "product-api",
			HMACKey:                       bytes.Repeat([]byte{0x42}, 32),
			SignerCallerID:                "lighter-signer",
			SignerHMACKey:                 bytes.Repeat([]byte{0x24}, 32),
			PublisherCallerID:             "account-publisher",
			PublisherHMACKey:              bytes.Repeat([]byte{0x66}, 32),
			SigningMaxRequestsPerMinute:   120,
			SigningMaxConcurrent:          4,
			PublisherMaxRequestsPerMinute: 600,
			PublisherMaxConcurrent:        4,
		},
		service:        service,
		store:          store,
		now:            service.now,
		signingSlots:   make(chan struct{}, 4),
		publisherSlots: make(chan struct{}, 4),
	}, lighter
}

func signedPublisherRequest(t *testing.T, server *server, path, body, nonce string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	timestamp := fmt.Sprintf("%d", server.now().Unix())
	digest := sha256.Sum256([]byte(body))
	canonical := fmt.Sprintf("POST\n%s\n%s\n%s\n%s\n%x", path, server.config.PublisherCallerID, timestamp, nonce, digest)
	mac := hmac.New(sha256.New, server.config.PublisherHMACKey)
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set("X-RTC-Caller", server.config.PublisherCallerID)
	request.Header.Set("X-RTC-Timestamp", timestamp)
	request.Header.Set("X-RTC-Nonce", nonce)
	request.Header.Set("X-RTC-Signature", hex.EncodeToString(mac.Sum(nil)))
	return request
}

func signedSignerRequest(t *testing.T, server *server, path, body, nonce string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	timestamp := fmt.Sprintf("%d", server.now().Unix())
	digest := sha256.Sum256([]byte(body))
	canonical := fmt.Sprintf("POST\n%s\n%s\n%s\n%s\n%x", path, server.config.SignerCallerID, timestamp, nonce, digest)
	mac := hmac.New(sha256.New, server.config.SignerHMACKey)
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set("X-RTC-Caller", server.config.SignerCallerID)
	request.Header.Set("X-RTC-Timestamp", timestamp)
	request.Header.Set("X-RTC-Nonce", nonce)
	request.Header.Set("X-RTC-Signature", hex.EncodeToString(mac.Sum(nil)))
	return request
}

func signedRequest(t *testing.T, server *server, path, body, nonce string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	timestamp := fmt.Sprintf("%d", server.now().Unix())
	digest := sha256.Sum256([]byte(body))
	canonical := fmt.Sprintf("POST\n%s\n%s\n%s\n%s\n%x", path, server.config.CallerID, timestamp, nonce, digest)
	mac := hmac.New(sha256.New, server.config.HMACKey)
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set("X-RTC-Caller", server.config.CallerID)
	request.Header.Set("X-RTC-Timestamp", timestamp)
	request.Header.Set("X-RTC-Nonce", nonce)
	request.Header.Set("X-RTC-Signature", hex.EncodeToString(mac.Sum(nil)))
	return request
}

func verifySignedResponse(t *testing.T, key []byte, caller, path, nonce string, response *httptest.ResponseRecorder) {
	t.Helper()
	provided, err := hex.DecodeString(response.Header().Get("X-RTC-Response-Signature"))
	if err != nil {
		t.Fatalf("decode response signature: %v", err)
	}
	digest := sha256.Sum256(response.Body.Bytes())
	canonical := fmt.Sprintf(
		"RESPONSE\n%s\n%s\n%s\n%d\n%x",
		path,
		caller,
		nonce,
		response.Code,
		digest,
	)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		t.Fatal("response signature did not bind the response")
	}
}

func TestHMACNonceCannotBeReplayed(t *testing.T) {
	server, _ := newTestServer()
	body := `{"executionAccountId":"11111111-1111-4111-8111-111111111111"}`
	nonce := strings.Repeat("n", 32)
	first := httptest.NewRecorder()
	server.handler().ServeHTTP(first, signedRequest(t, server, "/v1/links/status", body, nonce))
	if first.Code != http.StatusNotFound {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
	verifySignedResponse(t, server.config.HMACKey, server.config.CallerID, "/v1/links/status", nonce, first)
	second := httptest.NewRecorder()
	server.handler().ServeHTTP(second, signedRequest(t, server, "/v1/links/status", body, nonce))
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d body=%s", second.Code, second.Body.String())
	}
}

func TestPrepareReturnsOnlyPublicAssociationData(t *testing.T) {
	server, _ := newTestServer()
	body := `{"executionAccountId":"11111111-1111-4111-8111-111111111111","ownerAddress":"0x1111111111111111111111111111111111111111","apiKeyIndex":4}`
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, signedRequest(t, server, "/v1/links/prepare", body, strings.Repeat("a", 32)))
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	lower := strings.ToLower(response.Body.String())
	for _, forbidden := range []string{"generated-credential", "ciphertext", "encrypted", "kmskey", "private", "secret"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("response exposed %q: %s", forbidden, response.Body.String())
		}
	}
	if !strings.Contains(response.Body.String(), "messageToSign") || !strings.Contains(response.Body.String(), "publicKey") {
		t.Fatalf("missing public association fields: %s", response.Body.String())
	}
}

func TestRevocationRoutesExposeOnlyOwnerSignedTombstoneProof(t *testing.T) {
	server, lighter := newTestServer()
	active := activateTestCredential(t, server.service, lighter)
	lighter.registered = active.PublicKey
	lighter.nextNonce = 8
	body := fmt.Sprintf(`{"executionAccountId":%q}`, testExecutionID)
	prepared := httptest.NewRecorder()
	server.handler().ServeHTTP(
		prepared,
		signedRequest(t, server, "/v1/links/revoke/prepare", body, strings.Repeat("r", 32)),
	)
	if prepared.Code != http.StatusCreated {
		t.Fatalf("prepare status=%d body=%s", prepared.Code, prepared.Body.String())
	}
	verifySignedResponse(
		t,
		server.config.HMACKey,
		server.config.CallerID,
		"/v1/links/revoke/prepare",
		strings.Repeat("r", 32),
		prepared,
	)
	var revocation publicRevocation
	if err := json.Unmarshal(prepared.Body.Bytes(), &revocation); err != nil {
		t.Fatal(err)
	}
	if revocation.MessageToSign == "" || revocation.TombstonePublicKey == "" {
		t.Fatalf("missing revocation signature payload: %+v", revocation)
	}
	lower := strings.ToLower(prepared.Body.String())
	for _, forbidden := range []string{"generated-credential", "ciphertext", "encrypted", "kmskey", "private", "secret"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("revocation response exposed %q: %s", forbidden, prepared.Body.String())
		}
	}

	lighter.registered = revocation.TombstonePublicKey
	confirmBody := fmt.Sprintf(
		`{"executionAccountId":%q,"revocationId":%q,"l1Signature":%q}`,
		testExecutionID,
		revocation.RevocationID,
		validTestSignature(),
	)
	confirmed := httptest.NewRecorder()
	server.handler().ServeHTTP(
		confirmed,
		signedRequest(t, server, "/v1/links/revoke/confirm", confirmBody, strings.Repeat("c", 32)),
	)
	if confirmed.Code != http.StatusOK || !strings.Contains(confirmed.Body.String(), `"status":"revoked"`) {
		t.Fatalf("confirm status=%d body=%s", confirmed.Code, confirmed.Body.String())
	}
	verifySignedResponse(
		t,
		server.config.HMACKey,
		server.config.CallerID,
		"/v1/links/revoke/confirm",
		strings.Repeat("c", 32),
		confirmed,
	)

	status := httptest.NewRecorder()
	server.handler().ServeHTTP(
		status,
		signedRequest(t, server, "/v1/links/revoke/status", body, strings.Repeat("t", 32)),
	)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"registeredPublicKey"`) {
		t.Fatalf("status=%d body=%s", status.Code, status.Body.String())
	}
}

func TestSecretBearingPrepareFieldsAreRejected(t *testing.T) {
	server, _ := newTestServer()
	for index, field := range []string{"ethereumPrivateKey", "apiPrivateKey", "secretApiKey"} {
		body := fmt.Sprintf(`{"executionAccountId":"11111111-1111-4111-8111-111111111111","ownerAddress":"0x1111111111111111111111111111111111111111","apiKeyIndex":4,%q:"forbidden"}`, field)
		response := httptest.NewRecorder()
		nonce := fmt.Sprintf("%032d", index+1)
		server.handler().ServeHTTP(response, signedRequest(t, server, "/v1/links/prepare", body, nonce))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("field %s status = %d body=%s", field, response.Code, response.Body.String())
		}
	}
}

func TestPrepareRejectsUserSuppliedAccountCoordinates(t *testing.T) {
	server, _ := newTestServer()
	for index, field := range []string{`"accountIndex":42`, `"nonce":7`} {
		body := fmt.Sprintf(`{"executionAccountId":"11111111-1111-4111-8111-111111111111","ownerAddress":"0x1111111111111111111111111111111111111111","apiKeyIndex":4,%s}`, field)
		response := httptest.NewRecorder()
		nonce := fmt.Sprintf("coordinate-%023d", index)
		server.handler().ServeHTTP(response, signedRequest(t, server, "/v1/links/prepare", body, nonce))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("field %s status = %d body=%s", field, response.Code, response.Body.String())
		}
	}
}

func TestWithdrawalAndTransferRoutesDoNotExist(t *testing.T) {
	server, _ := newTestServer()
	for _, path := range []string{"/v1/withdraw", "/v1/transfer", "/api/v1/sendTx", "/v1/signer/modify-order", "/v1/signer/auth-token"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, http.NoBody)
		server.handler().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("path %s status = %d", path, response.Code)
		}
	}
}

func TestDisabledProvisionerIsNotReady(t *testing.T) {
	server := &server{config: config{}, now: time.Now}
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "disabled") {
		t.Fatalf("body = %s", body)
	}
}

func TestSigningBridgeIsAuthenticatedAndReplayProtected(t *testing.T) {
	server, lighter := newTestServer()
	link, err := server.service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		APIKeyIndex:        4,
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
	body := fmt.Sprintf(`{"executionAccountId":%q,"intentId":"intent-001","marketIndex":5,"clientOrderIndex":1,"baseAmount":10000,"price":2500,"isAsk":true,"orderType":0,"timeInForce":0,"reduceOnly":false,"triggerPrice":0,"orderExpiryMs":0,"transaction":{"nonce":8,"expiresAtMs":%d}}`, testExecutionID, server.now().Add(time.Minute).UnixMilli())
	nonce := strings.Repeat("s", 32)
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, signedSignerRequest(t, server, "/v1/signer/create-order", body, nonce))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"credentialVersion":1`) {
		t.Fatalf("signing response status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-RTC-Response-Signature") == "" {
		t.Fatal("signing response was not authenticated")
	}
	replay := httptest.NewRecorder()
	server.handler().ServeHTTP(replay, signedSignerRequest(t, server, "/v1/signer/create-order", body, nonce))
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	wrongCaller := httptest.NewRecorder()
	server.handler().ServeHTTP(wrongCaller, signedRequest(t, server, "/v1/signer/create-order", body, strings.Repeat("p", 32)))
	if wrongCaller.Code != http.StatusUnauthorized {
		t.Fatalf("product caller reached signing bridge: status=%d", wrongCaller.Code)
	}
}
