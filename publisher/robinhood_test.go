package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestDualRPCDisagreementAndReorgFailClosed(t *testing.T) {
	left := loadEndpointFixture(t)
	right := loadEndpointFixture(t)
	if !sameEndpointObservation(left, right) || !receiptsFinal(left.Receipts, left.Block) {
		t.Fatal("identical final observations should reconcile")
	}
	right.Block.Hash = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if sameEndpointObservation(left, right) {
		t.Fatal("RPC finalized hash disagreement must fail")
	}
	right = loadEndpointFixture(t)
	right.Receipts[0].BlockHash = "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if sameEndpointObservation(left, right) {
		t.Fatal("receipt reorg disagreement must fail")
	}
	left.Receipts[0].BlockNumber = "0x65"
	if receiptsFinal(left.Receipts, left.Block) {
		t.Fatal("receipt above finalized head must fail")
	}
}

func TestReceiptMustBelongToCanonicalGraph(t *testing.T) {
	observation := loadEndpointFixture(t)
	binding := RobinhoodBinding{
		Registry: "0x6666666666666666666666666666666666666666", Factory: "0x2222222222222222222222222222222222222222",
		Vault: "0x9999999999999999999999999999999999999999", RiskManager: "0x3333333333333333333333333333333333333333",
		SpotAdapter: "0x4444444444444444444444444444444444444444",
	}
	if !receiptsBound(observation.Receipts, binding) {
		t.Fatal("factory receipt should bind to the graph")
	}
	observation.Receipts[0].To = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if receiptsBound(observation.Receipts, binding) {
		t.Fatal("cross-account receipt substitution must fail")
	}
}

func TestRPCRateLimitFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	endpoint, err := newRPCEndpoint(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	var target string
	if err := endpoint.call(context.Background(), "eth_chainId", nil, &target); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limit, got %v", err)
	}
}

func loadEndpointFixture(t *testing.T) endpointObservation {
	t.Helper()
	encoded, err := os.ReadFile("testdata/robinhood-observation.json")
	if err != nil {
		t.Fatal(err)
	}
	var observation endpointObservation
	if err := json.Unmarshal(encoded, &observation); err != nil {
		t.Fatal(err)
	}
	return observation
}
