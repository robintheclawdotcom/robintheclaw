package publisher

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
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
