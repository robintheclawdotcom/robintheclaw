package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolverAuthenticatesAndValidatesBinding(t *testing.T) {
	key := []byte(strings.Repeat("b", 32))
	binding := validAccountBinding()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := json.Marshal(binding)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(body)
		canonical := fmt.Sprintf("RESPONSE\n/v1/signer/resolve\nrobinhood-signer\n%s\n200\n%x", request.Header.Get("X-RTC-Nonce"), digest)
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(canonical))
		w.Header().Set("X-RTC-Response-Signature", hex.EncodeToString(mac.Sum(nil)))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	resolver := newHTTPAccountResolver(server.URL, "robinhood-signer", key)
	result, err := resolver.Resolve(t.Context(), binding.ExecutionAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if result.BindingSHA256 != binding.BindingSHA256 || result.KMSKeyID != binding.KMSKeyID {
		t.Fatalf("unexpected resolved binding: %#v", result)
	}
}

func TestResolverRejectsCrossAccountResponse(t *testing.T) {
	key := []byte(strings.Repeat("b", 32))
	binding := validAccountBinding()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := json.Marshal(binding)
		digest := sha256.Sum256(body)
		canonical := fmt.Sprintf("RESPONSE\n/v1/signer/resolve\nrobinhood-signer\n%s\n200\n%x", request.Header.Get("X-RTC-Nonce"), digest)
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(canonical))
		w.Header().Set("X-RTC-Response-Signature", hex.EncodeToString(mac.Sum(nil)))
		_, _ = w.Write(body)
	}))
	defer server.Close()
	resolver := newHTTPAccountResolver(server.URL, "robinhood-signer", key)
	if _, err := resolver.Resolve(t.Context(), "22222222-2222-4222-8222-222222222222"); err == nil {
		t.Fatal("cross-account provisioner response was accepted")
	}
}

func validAccountBinding() accountBinding {
	value := accountBinding{
		ExecutionAccountID:  "11111111-1111-4111-8111-111111111111",
		OwnerAddress:        "0x0000000000000000000000000000000000000001",
		KMSKeyID:            "arn:aws:kms:region:account:key/test",
		SignerAddress:       "0x0000000000000000000000000000000000000002",
		KeyVersion:          1,
		FactoryAddress:      "0x0000000000000000000000000000000000000003",
		FactoryCodeHash:     "0x" + strings.Repeat("1", 64),
		RegistryAddress:     "0x0000000000000000000000000000000000000004",
		RegistryCodeHash:    "0x" + strings.Repeat("2", 64),
		PolicyDigest:        "0x" + strings.Repeat("3", 64),
		VaultAddress:        "0x0000000000000000000000000000000000000005",
		VaultCodeHash:       "0x" + strings.Repeat("4", 64),
		RiskManagerAddress:  "0x0000000000000000000000000000000000000006",
		RiskManagerCodeHash: "0x" + strings.Repeat("5", 64),
		SpotAdapterAddress:  "0x0000000000000000000000000000000000000007",
		SpotAdapterCodeHash: "0x" + strings.Repeat("6", 64),
	}
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	value.BindingSHA256 = hex.EncodeToString(digest[:])
	return value
}
