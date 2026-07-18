package publisher

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestSignedClientUsesValidUniqueReplayNonces(t *testing.T) {
	key := strings.Repeat("ab", 32)
	keyBytes, _ := hex.DecodeString(key)
	var mu sync.Mutex
	nonces := make(map[string]struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		digest := sha256.Sum256(body)
		canonical := strings.Join([]string{
			request.Method, request.URL.Path, request.Header.Get("X-RTC-Caller"),
			request.Header.Get("X-RTC-Timestamp"), request.Header.Get("X-RTC-Nonce"), hex.EncodeToString(digest[:]),
		}, "\n")
		mac := hmac.New(sha256.New, keyBytes)
		_, _ = mac.Write([]byte(canonical))
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(request.Header.Get("X-RTC-Signature"))) {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		nonce := request.Header.Get("X-RTC-Nonce")
		if _, exists := nonces[nonce]; exists {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		nonces[nonce] = struct{}{}
		writer.Header().Set(
			"X-RTC-Response-Signature",
			responseSignature(keyBytes, request.URL.Path, "account-publisher", nonce, http.StatusAccepted, nil),
		)
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	client, err := NewSignedClient(server.URL, "account-publisher", key, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := client.Post(context.Background(), "/v1/account-snapshots", []byte(`{"ok":true}`)); err != nil {
			t.Fatal(err)
		}
	}
	if len(nonces) != 2 {
		t.Fatalf("expected two nonces, got %d", len(nonces))
	}
}

func TestSignedClientAuthenticatesPersistenceResponseBeforeStatus(t *testing.T) {
	keyHex := strings.Repeat("ab", 32)
	key, _ := hex.DecodeString(keyHex)
	otherKey, _ := hex.DecodeString(strings.Repeat("cd", 32))
	const path = "/internal/v1/readiness"
	const caller = "account-publisher"

	tests := []struct {
		name          string
		actualStatus  int
		actualBody    []byte
		signature     bool
		signedStatus  int
		signedBody    []byte
		signedPath    string
		signedNonce   string
		signingKey    []byte
		wantRateLimit bool
		wantError     string
	}{
		{
			name: "valid", actualStatus: http.StatusAccepted, signature: true,
			signedStatus: http.StatusAccepted, signedPath: path, signingKey: key,
		},
		{
			name: "missing", actualStatus: http.StatusAccepted,
			wantError: "unauthenticated response",
		},
		{
			name: "body", actualStatus: http.StatusAccepted, actualBody: []byte(`{"stored":true}`), signature: true,
			signedStatus: http.StatusAccepted, signedBody: []byte(`{}`), signedPath: path, signingKey: key,
			wantError: "unauthenticated response",
		},
		{
			name: "status", actualStatus: http.StatusAccepted, signature: true,
			signedStatus: http.StatusOK, signedPath: path, signingKey: key,
			wantError: "unauthenticated response",
		},
		{
			name: "path", actualStatus: http.StatusAccepted, signature: true,
			signedStatus: http.StatusAccepted, signedPath: "/v1/account-snapshots", signingKey: key,
			wantError: "unauthenticated response",
		},
		{
			name: "nonce", actualStatus: http.StatusAccepted, signature: true,
			signedStatus: http.StatusAccepted, signedPath: path, signedNonce: strings.Repeat("n", 48), signingKey: key,
			wantError: "unauthenticated response",
		},
		{
			name: "key substitution", actualStatus: http.StatusAccepted, signature: true,
			signedStatus: http.StatusAccepted, signedPath: path, signingKey: otherKey,
			wantError: "unauthenticated response",
		},
		{
			name: "unsigned rate limit", actualStatus: http.StatusTooManyRequests,
			wantError: "unauthenticated response",
		},
		{
			name: "signed rate limit", actualStatus: http.StatusTooManyRequests, signature: true,
			signedStatus: http.StatusTooManyRequests, signedPath: path, signingKey: key, wantRateLimit: true,
		},
		{
			name: "oversized", actualStatus: http.StatusAccepted, actualBody: make([]byte, maxAuthenticatedResponseBytes+1), signature: true,
			signedStatus: http.StatusAccepted, signedPath: path, signingKey: key,
			wantError: "invalid authenticated response",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				nonce := request.Header.Get("X-RTC-Nonce")
				if test.signedNonce != "" {
					nonce = test.signedNonce
				}
				if test.signature {
					writer.Header().Set(
						"X-RTC-Response-Signature",
						responseSignature(
							test.signingKey,
							test.signedPath,
							caller,
							nonce,
							test.signedStatus,
							test.signedBody,
						),
					)
				}
				writer.WriteHeader(test.actualStatus)
				_, _ = writer.Write(test.actualBody)
			}))
			defer server.Close()
			client, err := NewSignedClient(server.URL, caller, keyHex, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			err = client.Post(context.Background(), path, []byte(`{"ready":true}`))
			if test.wantRateLimit {
				if !errors.Is(err, ErrRateLimited) {
					t.Fatalf("expected authenticated rate limit, got %v", err)
				}
				return
			}
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected %q, got %v", test.wantError, err)
			}
			if errors.Is(err, ErrRateLimited) {
				t.Fatal("unauthenticated status was interpreted")
			}
		})
	}
}

func responseSignature(key []byte, path, caller, nonce string, status int, body []byte) string {
	digest := sha256.Sum256(body)
	canonical := strings.Join([]string{
		"RESPONSE", path, caller, nonce, strconv.Itoa(status), hex.EncodeToString(digest[:]),
	}, "\n")
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}
