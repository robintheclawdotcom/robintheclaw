package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthorizationRejectsReplay(t *testing.T) {
	store := &fakeStore{}
	key := []byte(strings.Repeat("k", 32))
	caller := "product-api"
	body := []byte(`{"executionAccountId":"11111111-1111-4111-8111-111111111111"}`)
	now := time.Now().UTC()
	request := signedRequest(body, key, caller, strings.Repeat("n", 32), now)
	authorized, err := authorizeRequest(context.Background(), store, request, body, key, caller, now)
	if err != nil || !authorized {
		t.Fatalf("valid request rejected: authorized=%v err=%v", authorized, err)
	}
	replay := signedRequest(body, key, caller, strings.Repeat("n", 32), now)
	authorized, err = authorizeRequest(context.Background(), store, replay, body, key, caller, now)
	if err != nil || authorized {
		t.Fatalf("replayed request accepted: authorized=%v err=%v", authorized, err)
	}
}

func signedRequest(body, key []byte, caller, nonce string, now time.Time) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/graphs/status", strings.NewReader(string(body)))
	timestamp := fmt.Sprintf("%d", now.Unix())
	digest := sha256.Sum256(body)
	canonical := fmt.Sprintf("POST\n/v1/graphs/status\n%s\n%s\n%s\n%x", caller, timestamp, nonce, digest)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set("X-RTC-Caller", caller)
	request.Header.Set("X-RTC-Timestamp", timestamp)
	request.Header.Set("X-RTC-Nonce", nonce)
	request.Header.Set("X-RTC-Signature", hex.EncodeToString(mac.Sum(nil)))
	return request
}
