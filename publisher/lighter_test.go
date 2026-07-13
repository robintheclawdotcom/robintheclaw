package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type lighterFixture struct {
	Account json.RawMessage `json:"account"`
	Orders  json.RawMessage `json:"orders"`
	Trades  json.RawMessage `json:"trades"`
	Nonce   json.RawMessage `json:"nonce"`
}

func TestLighterRESTReconstruction(t *testing.T) {
	fixture := loadLighterFixture(t)
	server := lighterServer(t, fixture, 0)
	defer server.Close()
	binding := lighterTestBinding(t)
	client, err := NewLighterClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	observation, err := client.Collect(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if observation.AccountIndex != 77 || observation.Nonce != 42 || observation.ExpectedNonce != 42 ||
		observation.MaintenanceMarginRatioMicros != 4_000_000 || !observation.NoUnknownOrders ||
		!observation.NoUnknownPositions || !observation.Flat || !observation.CollateralReady || !observation.RESTReconstructed {
		t.Fatalf("unexpected observation: %+v", observation)
	}
}

func TestLighterRejectsCrossAccountOrder(t *testing.T) {
	fixture := loadLighterFixture(t)
	fixture.Orders = json.RawMessage(`{"code":200,"orders":[{"market_index":5,"owner_account_index":78,"status":"open"}]}`)
	server := lighterServer(t, fixture, 0)
	defer server.Close()
	client, _ := NewLighterClient(server.URL, server.Client())
	_, err := client.Collect(context.Background(), lighterTestBinding(t))
	if err == nil {
		t.Fatal("expected account substitution to fail")
	}
}

func TestLighterPropagatesRateLimit(t *testing.T) {
	server := lighterServer(t, loadLighterFixture(t), http.StatusTooManyRequests)
	defer server.Close()
	client, _ := NewLighterClient(server.URL, server.Client())
	_, err := client.Collect(context.Background(), lighterTestBinding(t))
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limit, got %v", err)
	}
}

func TestLighterRejectsTradingAuthToken(t *testing.T) {
	if validLighterReadOnlyToken("1999999999:77:4:aaaaaaaaaaaaaaaa", 77, time.Now()) {
		t.Fatal("transaction-capable auth token must be rejected")
	}
}

func loadLighterFixture(t *testing.T) lighterFixture {
	t.Helper()
	encoded, err := os.ReadFile("testdata/lighter.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture lighterFixture
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func lighterServer(t *testing.T, fixture lighterFixture, forcedStatus int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if forcedStatus != 0 {
			writer.WriteHeader(forcedStatus)
			return
		}
		if request.Header.Get("Authorization") != "ro:77:single:1999999999:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		var response json.RawMessage
		switch request.URL.Path {
		case "/api/v1/account":
			response = fixture.Account
		case "/api/v1/accountActiveOrders":
			response = fixture.Orders
		case "/api/v1/trades":
			response = fixture.Trades
		case "/api/v1/nextNonce":
			response = fixture.Nonce
		default:
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(response)
	}))
	return server
}

func lighterTestBinding(t *testing.T) LighterBinding {
	t.Helper()
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	nonce := filepath.Join(dir, "nonce")
	if err := os.WriteFile(token, []byte("ro:77:single:1999999999:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nonce, []byte("42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return LighterBinding{AccountIndex: 77, APIKeyIndex: 4, MarketID: 5, ReadOnlyTokenFile: token, ExpectedNonceFile: nonce, MinimumCollateralRaw: "50"}
}
