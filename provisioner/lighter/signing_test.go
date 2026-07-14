package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elliottech/lighter-go/types/txtypes"
)

func TestSignedOrderRejectsAssetMovementTransactionType(t *testing.T) {
	transaction := signedTransaction{
		AccountIndex: 7,
		APIKeyIndex:  4,
		TxType:       txtypes.TxTypeL2Withdraw,
		TxHash:       "0x01",
		TxInfo:       json.RawMessage(`{"AccountIndex":7,"ApiKeyIndex":4}`),
	}
	if validateSignedIdentity(transaction, txtypes.TxTypeL2CreateOrder) == nil {
		t.Fatal("withdrawal transaction type was accepted as an order")
	}
}

func TestSignedResultRejectsCredentialMismatch(t *testing.T) {
	record := credential{
		ExecutionAccountID: testExecutionID,
		AccountIndex:       7,
		APIKeyIndex:        4,
		Version:            2,
	}
	transaction := signedTransaction{
		ExecutionAccountID: testExecutionID,
		AccountIndex:       8,
		APIKeyIndex:        4,
		CredentialVersion:  2,
		IntentID:           "intent-001",
		TxType:             txtypes.TxTypeL2CreateOrder,
		TxHash:             "0x01",
		TxInfo:             json.RawMessage(`{"AccountIndex":8,"ApiKeyIndex":4}`),
	}
	if validateSignedResult(record, transaction.IntentID, txtypes.TxTypeL2CreateOrder, transaction) == nil {
		t.Fatal("credential account mismatch was accepted")
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

func TestCreateOrderPolicyRejectsSubstitutionAndOversize(t *testing.T) {
	request := createOrderRequest{
		MarketIndex: 5, ClientOrderID: 1, BaseAmount: 10_000, Price: 2_500,
		IsAsk: true, OrderType: 0, TimeInForce: 0,
	}
	if err := validateCreateOrderPolicy(request, 5, 4, 2); err != nil {
		t.Fatal(err)
	}
	request.MarketIndex = 6
	if err := validateCreateOrderPolicy(request, 5, 4, 2); err == nil {
		t.Fatal("market substitution was accepted")
	}
	request.MarketIndex = 5
	request.BaseAmount++
	if err := validateCreateOrderPolicy(request, 5, 4, 2); err == nil {
		t.Fatal("oversize order was accepted")
	}
	request.IsAsk = false
	request.ReduceOnly = true
	if err := validateCreateOrderPolicy(request, 5, 4, 2); err != nil {
		t.Fatalf("oversize reduce-only unwind was rejected: %v", err)
	}
	request.IsAsk = true
	if err := validateCreateOrderPolicy(request, 5, 4, 2); err == nil {
		t.Fatal("reduce-only ask was accepted")
	}
}

func TestUnresolvedSigningClaimBlocksRetryAndNextNonce(t *testing.T) {
	store := newMemoryStore()
	record := credential{
		ID:                 "credential-1",
		ExecutionAccountID: testExecutionID,
		AccountIndex:       42,
		APIKeyIndex:        4,
		Version:            1,
		ChangeNonce:        7,
		Status:             statusLinked,
	}
	store.records[record.ID] = record
	store.bindings[record.ExecutionAccountID] = binding{
		ExecutionAccountID: record.ExecutionAccountID,
		AccountIndex:       record.AccountIndex,
		APIKeyIndex:        record.APIKeyIndex,
		Status:             "linked",
		ActiveCredentialID: record.ID,
	}

	if cached, err := store.ClaimSigningNonce(context.Background(), record, "intent-001", 8, strings.Repeat("a", 64)); err != nil || cached != nil {
		t.Fatalf("initial claim failed: cached=%v err=%v", cached, err)
	}
	if _, err := store.ClaimSigningNonce(context.Background(), record, "intent-001", 8, strings.Repeat("a", 64)); err == nil || !strings.Contains(err.Error(), "no durable result") {
		t.Fatalf("unresolved exact retry returned %v", err)
	}
	if _, err := store.ClaimSigningNonce(context.Background(), record, "intent-002", 9, strings.Repeat("b", 64)); err == nil || !strings.Contains(err.Error(), "no durable result") {
		t.Fatalf("next nonce after unresolved claim returned %v", err)
	}
}
