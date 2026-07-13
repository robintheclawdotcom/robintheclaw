package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elliottech/lighter-go/client"
	"github.com/elliottech/lighter-go/types/txtypes"
)

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

func TestSignerSurfaceHasNoAssetMovementRoute(t *testing.T) {
	server, err := newSignerServer(config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/sign/withdraw", "/v1/sign/transfer", "/v1/sign/update-leverage"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		response := httptest.NewRecorder()
		server.routes().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s got status %d", path, response.Code)
		}
	}
}

func TestIOCLimitOrderUsesNilOrderExpiry(t *testing.T) {
	transaction := &txtypes.L2CreateOrderTxInfo{
		AccountIndex: 1,
		ApiKeyIndex:  2,
		OrderInfo: &txtypes.OrderInfo{
			MarketIndex:      0,
			ClientOrderIndex: 1,
			BaseAmount:       1,
			Price:            1,
			IsAsk:            1,
			Type:             txtypes.LimitOrder,
			TimeInForce:      txtypes.ImmediateOrCancel,
			OrderExpiry:      txtypes.NilOrderExpiry,
		},
		ExpiredAt: 1,
		Nonce:     0,
	}
	if err := transaction.Validate(); err != nil {
		t.Fatalf("valid IOC limit order rejected: %v", err)
	}
	transaction.OrderExpiry = time.Now().Add(time.Minute).UnixMilli()
	if err := transaction.Validate(); err != txtypes.ErrOrderExpiryInvalid {
		t.Fatalf("non-zero IOC order expiry returned %v", err)
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
	t.Setenv("LIGHTER_API_PRIVATE_KEY", "test-private-key")
	t.Setenv("LIGHTER_CHAIN_ID", "300")
	t.Setenv("LIGHTER_ACCOUNT_INDEX", "1")
	t.Setenv("LIGHTER_API_KEY_INDEX", "2")
	value, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(value.apiHMACKey) != sha256.Size || value.callerID != "execution-coordinator" {
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
	}
	return &signerServer{
		config: value,
		client: &client.TxClient{},
		now:    func() time.Time { return now },
		slots:  make(chan struct{}, value.maxConcurrentRequests),
		nonces: authNonces{expires: make(map[string]time.Time)},
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
