package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBridgeAuthenticatesRequestAndResponse(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	caller := "lighter-signer"
	now := time.Unix(1_800_000_000, 0)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		digest := sha256.Sum256(body)
		canonical := fmt.Sprintf(
			"POST\n%s\n%s\n%s\n%s\n%x",
			request.URL.Path,
			caller,
			request.Header.Get("X-RTC-Timestamp"),
			request.Header.Get("X-RTC-Nonce"),
			digest,
		)
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(canonical))
		if request.Header.Get("X-RTC-Caller") != caller || request.Header.Get("X-RTC-Signature") != hex.EncodeToString(mac.Sum(nil)) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		response := []byte(`{"status":"signed"}`)
		responseDigest := sha256.Sum256(response)
		responseCanonical := fmt.Sprintf(
			"RESPONSE\n%s\n%s\n%s\n%d\n%x",
			request.URL.Path,
			caller,
			request.Header.Get("X-RTC-Nonce"),
			http.StatusOK,
			responseDigest,
		)
		mac = hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(responseCanonical))
		w.Header().Set("X-RTC-Response-Signature", hex.EncodeToString(mac.Sum(nil)))
		_, _ = w.Write(response)
	}))
	defer upstream.Close()

	bridge := newHTTPBridge(upstream.URL, caller, key)
	bridge.now = func() time.Time { return now }
	bridge.random = bytes.NewReader(bytes.Repeat([]byte{0x24}, 24))
	var response struct {
		Status string `json:"status"`
	}
	if err := bridge.call(context.Background(), "/v1/signer/create-order", map[string]string{"executionAccountId": testBridgeAccountID}, &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "signed" {
		t.Fatalf("status = %q", response.Status)
	}
}

func TestBridgeRejectsUnauthenticatedResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"forged"}`))
	}))
	defer upstream.Close()
	bridge := newHTTPBridge(upstream.URL, "lighter-signer", bytes.Repeat([]byte{0x42}, 32))
	bridge.random = bytes.NewReader(bytes.Repeat([]byte{0x24}, 24))
	var response map[string]string
	if err := bridge.call(context.Background(), "/v1/signer/create-order", map[string]string{"executionAccountId": testBridgeAccountID}, &response); err == nil {
		t.Fatal("unsigned response was accepted")
	}
}

const testBridgeAccountID = "11111111-1111-4111-8111-111111111111"
