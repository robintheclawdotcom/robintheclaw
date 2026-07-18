package main

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

type stubBridge struct {
	callFn func(string, any, any) error
}

func (value stubBridge) call(_ context.Context, path string, input, output any) error {
	return value.callFn(path, input, output)
}

func TestDisabledSignerFailsClosed(t *testing.T) {
	server, err := newSignerServer(config{})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/sign/create-order", strings.NewReader("{}"))
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response = httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz got status %d", response.Code)
	}
}

func TestLivezDoesNotClaimReadiness(t *testing.T) {
	server, err := newSignerServer(config{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/livez", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("livez got status %d", response.Code)
	}
}

func TestEveryResponseIsAuthenticated(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	server := authTestServer(now)
	body := []byte(`{"executionAccountId":"11111111-1111-4111-8111-111111111111","intentId":"intent-1","marketIndex":1,"clientOrderIndex":1,"baseAmount":1,"price":1,"isAsk":true,"orderType":0,"timeInForce":0,"reduceOnly":false,"triggerPrice":0,"orderExpiryMs":0,"transaction":{"nonce":1,"expiresAtMs":1800000300000}}`)

	for name, request := range map[string]*http.Request{
		"success": signedRequest(server.config, body, strings.Repeat("s", 32), now),
		"authorization rejection": func() *http.Request {
			request := httptest.NewRequest(http.MethodPost, "/v1/sign/create-order", strings.NewReader(string(body)))
			request.Header.Set("X-RTC-Caller", server.config.callerID)
			request.Header.Set("X-RTC-Nonce", strings.Repeat("u", 32))
			return request
		}(),
		"unknown route": func() *http.Request {
			request := httptest.NewRequest(http.MethodPost, "/v1/sign/withdraw", strings.NewReader("{}"))
			request.Header.Set("X-RTC-Nonce", strings.Repeat("n", 32))
			return request
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.routes().ServeHTTP(response, request)
			assertResponseSignature(t, server.config, request, response)
		})
	}
}

func TestSignerSurfaceHasNoAssetMovementRoute(t *testing.T) {
	server, err := newSignerServer(config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/sign/withdraw", "/v1/sign/transfer", "/v1/sign/modify-order", "/v1/sign/update-leverage", "/v1/auth-token"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		response := httptest.NewRecorder()
		server.routes().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s got status %d", path, response.Code)
		}
	}
}

func TestReservedAPIKeyIndexIsRejected(t *testing.T) {
	transaction := signedTransaction{
		ExecutionAccountID: "account-canary-1",
		AccountIndex:       7,
		APIKeyIndex:        3,
		CredentialVersion:  1,
		IntentID:           "intent-1",
		TxType:             14,
		TxHash:             "0x01",
		TxInfo:             json.RawMessage(`{"AccountIndex":7,"ApiKeyIndex":3}`),
	}
	if validateSignedTransaction(transaction, transaction.ExecutionAccountID, transaction.IntentID, txTypeCreateOrder) == nil {
		t.Fatal("reserved API key index was accepted")
	}
}

func TestSignedTransactionCannotSubstituteAssetMovementType(t *testing.T) {
	transaction := signedTransaction{
		ExecutionAccountID: "account-canary-1",
		AccountIndex:       7,
		APIKeyIndex:        4,
		CredentialVersion:  1,
		IntentID:           "intent-1",
		TxType:             13,
		TxHash:             "0x01",
		TxInfo:             json.RawMessage(`{"AccountIndex":7,"ApiKeyIndex":4}`),
	}
	if validateSignedTransaction(transaction, transaction.ExecutionAccountID, transaction.IntentID, txTypeCreateOrder) == nil {
		t.Fatal("withdrawal transaction type was accepted as an order")
	}
}

func TestSignedRequestIsAcceptedOnce(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	server := authTestServer(now)
	handler := server.authorize(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	body := []byte(`{"intentId":"intent-1"}`)
	nonce := strings.Repeat("n", 32)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, signedRequest(server.config, body, nonce, now))
	if first.Code != http.StatusNoContent {
		t.Fatalf("signed request got status %d", first.Code)
	}

	replay := httptest.NewRecorder()
	handler.ServeHTTP(replay, signedRequest(server.config, body, nonce, now))
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replayed request got status %d", replay.Code)
	}
}

func TestSignedRequestRejectsBodyTampering(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	server := authTestServer(now)
	handler := server.authorize(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := signedRequest(server.config, []byte(`{"intentId":"intent-1"}`), strings.Repeat("t", 32), now)
	request.Body = io.NopCloser(strings.NewReader(`{"intentId":"intent-2"}`))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("tampered request got status %d", response.Code)
	}
}

func TestSignedRequestRejectsTimestampOutsideWindow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for name, signedAt := range map[string]time.Time{
		"stale":  now.Add(-31 * time.Second),
		"future": now.Add(31 * time.Second),
	} {
		t.Run(name, func(t *testing.T) {
			server := authTestServer(now)
			handler := server.authorize(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, signedRequest(
				server.config,
				[]byte(`{"intentId":"intent-1"}`),
				strings.Repeat(name[:1], 32),
				signedAt,
			))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("request got status %d", response.Code)
			}
		})
	}
}

func TestSignerRateLimit(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	server := authTestServer(now)
	server.config.maxRequestsPerMinute = 1
	handler := server.authorize(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	body := []byte(`{"intentId":"intent-1"}`)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, signedRequest(server.config, body, strings.Repeat("a", 32), now))
	if first.Code != http.StatusNoContent {
		t.Fatalf("first request got status %d", first.Code)
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, signedRequest(server.config, body, strings.Repeat("b", 32), now))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request got status %d", second.Code)
	}
}

func TestSignerConcurrencyLimit(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	server := authTestServer(now)
	entered := make(chan struct{})
	release := make(chan struct{})
	handler := server.authorize(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	body := []byte(`{"intentId":"intent-1"}`)
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(first, signedRequest(server.config, body, strings.Repeat("c", 32), now))
	}()
	<-entered

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, signedRequest(server.config, body, strings.Repeat("d", 32), now))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("concurrent request got status %d", second.Code)
	}
	close(release)
	<-done
	if first.Code != http.StatusNoContent {
		t.Fatalf("first request got status %d", first.Code)
	}
}

func TestUnknownExecutionAccountIsRejectedBeforeSigning(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	server := authTestServer(now)
	body := []byte(`{"executionAccountId":"account-canary-2","intentId":"intent-1","marketIndex":1,"clientOrderIndex":1,"baseAmount":1,"price":1,"isAsk":true,"orderType":0,"timeInForce":0,"reduceOnly":false,"triggerPrice":0,"orderExpiryMs":0,"transaction":{"nonce":1,"expiresAtMs":1800000300000}}`)
	request := signedRequest(server.config, body, strings.Repeat("u", 32), now)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown account got status %d", response.Code)
	}
}

func TestProvisionerCrossAccountSubstitutionFailsClosed(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	server := authTestServer(now)
	server.bridge = stubBridge{callFn: func(_ string, input, output any) error {
		request := input.(createOrderRequest)
		response := output.(*signedTransaction)
		*response = signedTransaction{
			ExecutionAccountID: "22222222-2222-4222-8222-222222222222",
			AccountIndex:       42,
			APIKeyIndex:        4,
			CredentialVersion:  1,
			IntentID:           request.IntentID,
			TxType:             14,
			TxHash:             "0x01",
			TxInfo:             json.RawMessage(`{"AccountIndex":42,"ApiKeyIndex":4}`),
		}
		return nil
	}}
	body := []byte(`{"executionAccountId":"11111111-1111-4111-8111-111111111111","intentId":"intent-1","marketIndex":1,"clientOrderIndex":1,"baseAmount":1,"price":1,"isAsk":true,"orderType":0,"timeInForce":0,"reduceOnly":false,"triggerPrice":0,"orderExpiryMs":0,"transaction":{"nonce":1,"expiresAtMs":1800000300000}}`)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, signedRequest(server.config, body, strings.Repeat("x", 32), now))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("substituted account got status %d body=%s", response.Code, response.Body.String())
	}
}

func TestSignerUsesRotatedCredentialWithoutSecretConfiguration(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	server := authTestServer(now)
	version := int64(0)
	server.bridge = stubBridge{callFn: func(_ string, input, output any) error {
		version++
		request := input.(createOrderRequest)
		response := output.(*signedTransaction)
		*response = signedTransaction{
			ExecutionAccountID: request.ExecutionAccountID,
			AccountIndex:       42,
			APIKeyIndex:        4,
			CredentialVersion:  version,
			IntentID:           request.IntentID,
			TxType:             14,
			TxHash:             fmt.Sprintf("0x%02d", version),
			TxInfo:             json.RawMessage(`{"AccountIndex":42,"ApiKeyIndex":4}`),
		}
		return nil
	}}
	body := []byte(`{"executionAccountId":"11111111-1111-4111-8111-111111111111","intentId":"intent-1","marketIndex":1,"clientOrderIndex":1,"baseAmount":1,"price":1,"isAsk":true,"orderType":0,"timeInForce":0,"reduceOnly":false,"triggerPrice":0,"orderExpiryMs":0,"transaction":{"nonce":1,"expiresAtMs":1800000300000}}`)
	for index, nonce := range []string{strings.Repeat("r", 32), strings.Repeat("s", 32)} {
		response := httptest.NewRecorder()
		server.routes().ServeHTTP(response, signedRequest(server.config, body, nonce, now))
		if response.Code != http.StatusOK {
			t.Fatalf("request %d got status %d body=%s", index, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), fmt.Sprintf(`"credentialVersion":%d`, index+1)) {
			t.Fatalf("request %d did not use credential version %d: %s", index, index+1, response.Body.String())
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	server, err := newSignerServer(config{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/livez", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cache control header missing")
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("content type protection header missing")
	}
}

func TestLoadConfigRequiresHMACAuthentication(t *testing.T) {
	t.Setenv("LIGHTER_SIGNER_ENABLED", "true")
	t.Setenv("LIGHTER_SIGNER_HMAC_KEY", strings.Repeat("61", 32))
	t.Setenv("SIGNER_CALLER_ID", "execution-coordinator")
	t.Setenv("LIGHTER_PROVISIONER_URL", "https://provisioner.invalid")
	t.Setenv("LIGHTER_SIGNER_BRIDGE_HMAC_KEY", strings.Repeat("62", 32))
	t.Setenv("LIGHTER_SIGNER_BRIDGE_CALLER_ID", "lighter-signer")
	t.Setenv("LIGHTER_AAPL_MARKET_INDEX", "1")
	t.Setenv("LIGHTER_AAPL_BASE_DECIMALS", "0")
	t.Setenv("LIGHTER_AAPL_PRICE_DECIMALS", "0")
	value, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(value.apiHMACKey) != sha256.Size || len(value.bridgeHMACKey) != sha256.Size || value.callerID != "execution-coordinator" {
		t.Fatal("HMAC configuration was not loaded")
	}

	t.Setenv("LIGHTER_SIGNER_HMAC_KEY", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing HMAC key was accepted")
	}
}

func authTestServer(now time.Time) *signerServer {
	value := config{
		enabled:               true,
		apiHMACKey:            []byte(strings.Repeat("k", sha256.Size)),
		callerID:              "execution-coordinator",
		maxRequestsPerMinute:  60,
		maxConcurrentRequests: 1,
		marketIndex:           1,
		baseDecimals:          0,
		priceDecimals:         0,
	}
	return &signerServer{
		config: value,
		bridge: stubBridge{callFn: func(_ string, input, output any) error {
			request := input.(createOrderRequest)
			response := output.(*signedTransaction)
			*response = signedTransaction{
				ExecutionAccountID: request.ExecutionAccountID,
				AccountIndex:       7,
				APIKeyIndex:        4,
				CredentialVersion:  1,
				IntentID:           request.IntentID,
				TxType:             14,
				TxHash:             "0x01",
				TxInfo:             json.RawMessage(`{"AccountIndex":7,"ApiKeyIndex":4}`),
			}
			return nil
		}},
		now:    func() time.Time { return now },
		slots:  make(chan struct{}, value.maxConcurrentRequests),
		nonces: authNonces{expires: make(map[string]time.Time)},
	}
}

func TestCreateOrderPolicyPinsMarketShapeAndCap(t *testing.T) {
	valid := createOrderRequest{
		MarketIndex: 101, ClientOrderID: 1, BaseAmount: 10_000, Price: 2_500,
		IsAsk: true, OrderType: 0, TimeInForce: 0,
	}
	if err := validateCreateOrderPolicy(valid, 101, 4, 2); err != nil {
		t.Fatal(err)
	}
	unwind := valid
	unwind.IsAsk = false
	unwind.ReduceOnly = true
	unwind.Price++
	if err := validateCreateOrderPolicy(unwind, 101, 4, 2); err != nil {
		t.Fatalf("appreciated reduce-only unwind was rejected: %v", err)
	}
	for name, mutate := range map[string]func(*createOrderRequest){
		"market substitution": func(value *createOrderRequest) { value.MarketIndex++ },
		"oversize":            func(value *createOrderRequest) { value.BaseAmount++ },
		"non-IOC":             func(value *createOrderRequest) { value.TimeInForce = 1 },
		"trigger":             func(value *createOrderRequest) { value.TriggerPrice = 1 },
		"expiry":              func(value *createOrderRequest) { value.OrderExpiryMS = 1 },
		"long entry":          func(value *createOrderRequest) { value.IsAsk = false },
		"base encoding":       func(value *createOrderRequest) { value.BaseAmount = maximumLighterOrderValue + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if err := validateCreateOrderPolicy(request, 101, 4, 2); err == nil {
				t.Fatal("invalid order policy was accepted")
			}
		})
	}
	maximumEntry := valid
	maximumEntry.BaseAmount = maximumLighterOrderValue
	maximumEntry.Price = ^uint32(0)
	if err := validateCreateOrderPolicy(maximumEntry, 101, 0, 0); err == nil {
		t.Fatal("overflow-sized order was accepted")
	}
	maximumUnwind := maximumEntry
	maximumUnwind.IsAsk = false
	maximumUnwind.ReduceOnly = true
	if err := validateCreateOrderPolicy(maximumUnwind, 101, 0, 0); err != nil {
		t.Fatalf("protocol-valid reduce-only unwind was rejected: %v", err)
	}
}

func signedRequest(value config, body []byte, nonce string, signedAt time.Time) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/sign/create-order", strings.NewReader(string(body)))
	timestamp := fmt.Sprintf("%d", signedAt.Unix())
	request.Header.Set("X-RTC-Caller", value.callerID)
	request.Header.Set("X-RTC-Timestamp", timestamp)
	request.Header.Set("X-RTC-Nonce", nonce)
	bodyDigest := sha256.Sum256(body)
	canonical := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%s\n%x",
		request.Method,
		request.URL.Path,
		value.callerID,
		timestamp,
		nonce,
		bodyDigest,
	)
	mac := hmac.New(sha256.New, value.apiHMACKey)
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set("X-RTC-Signature", hex.EncodeToString(mac.Sum(nil)))
	return request
}

func assertResponseSignature(t *testing.T, value config, request *http.Request, response *httptest.ResponseRecorder) {
	t.Helper()
	digest := sha256.Sum256(response.Body.Bytes())
	canonical := fmt.Sprintf(
		"RESPONSE\n%s\n%s\n%s\n%d\n%x",
		request.URL.Path,
		value.callerID,
		request.Header.Get("X-RTC-Nonce"),
		response.Code,
		digest,
	)
	mac := hmac.New(sha256.New, value.apiHMACKey)
	_, _ = mac.Write([]byte(canonical))
	expected := hex.EncodeToString(mac.Sum(nil))
	if response.Header().Get("X-RTC-Response-Signature") != expected {
		t.Fatal("response signature is missing or invalid")
	}
}
