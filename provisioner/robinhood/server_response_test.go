package main

import (
	"bytes"
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

func TestProvisioningResponsesAreAuthenticated(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	value := &server{
		config: config{
			CallerID:   "robin-api",
			APIHMACKey: key,
		},
		now: time.Now,
	}
	for _, path := range []string{
		"/v1/graphs/prepare",
		"/v1/graphs/status",
		"/v1/graphs/confirm",
	} {
		nonce := strings.Repeat("n", 32)
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		request.Header.Set("X-RTC-Nonce", nonce)
		response := httptest.NewRecorder()

		value.handler().ServeHTTP(response, request)

		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d", path, response.Code)
		}
		verifyResponseHMAC(t, key, "robin-api", path, nonce, response)
	}
}

func verifyResponseHMAC(
	t *testing.T,
	key []byte,
	caller string,
	path string,
	nonce string,
	response *httptest.ResponseRecorder,
) {
	t.Helper()
	signature, err := hex.DecodeString(response.Header().Get("X-RTC-Response-Signature"))
	if err != nil || len(signature) != sha256.Size {
		t.Fatalf("invalid response signature: %v", err)
	}
	digest := sha256.Sum256(response.Body.Bytes())
	canonical := fmt.Sprintf(
		"RESPONSE\n%s\n%s\n%s\n%d\n%x",
		path,
		caller,
		nonce,
		response.Code,
		digest,
	)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		t.Fatal("response signature does not match")
	}
}
