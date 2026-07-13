package main

import (
	"testing"
	"time"

	"github.com/elliottech/lighter-go/types/txtypes"
)

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
