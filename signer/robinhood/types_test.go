package main

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

func validRequest() ExecuteRequest {
	return ExecuteRequest{
		ExecutionAccountID: "account-canary-1",
		RequestID:          "request-1",
		Intent: SpotIntentRequest{
			ID:            "0x" + "11" + string(bytes.Repeat([]byte{'0'}, 62)),
			StockToken:    "0x0000000000000000000000000000000000000001",
			Side:          "buy_spot",
			AmountIn:      "1000000",
			MinAmountOut:  "900000",
			Deadline:      2_000_000_000,
			ConfigVersion: 1,
		},
	}
}

func TestPackExecuteSpotUsesFixedSelector(t *testing.T) {
	intent, _, _, err := validRequest().validate()
	if err != nil {
		t.Fatal(err)
	}
	data, err := packExecuteSpot(intent)
	if err != nil {
		t.Fatal(err)
	}
	expected := crypto.Keccak256([]byte("executeSpot((bytes32,address,uint8,uint128,uint128,uint64,uint64))"))[:4]
	if !bytes.Equal(data[:4], expected) {
		t.Fatalf("unexpected selector: %x", data[:4])
	}
}

func TestIntentRejectsOverflowAndUnknownFields(t *testing.T) {
	request := validRequest()
	request.Intent.AmountIn = new(big.Int).Lsh(big.NewInt(1), 128).String()
	if _, _, _, err := request.validate(); err == nil {
		t.Fatal("uint128 overflow was accepted")
	}
	request = validRequest()
	request.Intent.Side = "short"
	if _, _, _, err := request.validate(); err == nil {
		t.Fatal("unknown side was accepted")
	}
}

func TestReplacementIdentityCannotSelfReference(t *testing.T) {
	request := validRequest()
	request.ReplacesRequestID = request.RequestID
	if _, _, _, err := request.validate(); err == nil {
		t.Fatal("self-referential replacement was accepted")
	}
}

func TestFeeBumpIsStrict(t *testing.T) {
	for _, value := range []*big.Int{big.NewInt(1), big.NewInt(8), big.NewInt(1_000_000)} {
		if bumped(value).Cmp(value) <= 0 {
			t.Fatalf("fee did not increase: %s", value)
		}
	}
}
