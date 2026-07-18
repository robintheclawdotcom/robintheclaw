package scheduler

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunnerClientAuthenticatesResponseBeforeUse(t *testing.T) {
	key := bytes.Repeat([]byte{0x31}, 32)
	body := []byte(`{"kind":"entry"}`)
	tests := []struct {
		name      string
		signature func(*http.Request) string
	}{
		{
			name: "missing",
			signature: func(*http.Request) string {
				return ""
			},
		},
		{
			name: "wrong key",
			signature: func(request *http.Request) string {
				return hex.EncodeToString(responseMAC(bytes.Repeat([]byte{0x32}, 32), request.URL.Path,
					"scheduler", request.Header.Get("X-Robin-Nonce"), http.StatusOK, body))
			},
		},
		{
			name: "path substitution",
			signature: func(request *http.Request) string {
				return hex.EncodeToString(responseMAC(key, "/v1/other", "scheduler",
					request.Header.Get("X-Robin-Nonce"), http.StatusOK, body))
			},
		},
		{
			name: "caller substitution",
			signature: func(request *http.Request) string {
				return hex.EncodeToString(responseMAC(key, request.URL.Path, "other-scheduler",
					request.Header.Get("X-Robin-Nonce"), http.StatusOK, body))
			},
		},
		{
			name: "nonce substitution",
			signature: func(request *http.Request) string {
				return hex.EncodeToString(responseMAC(key, request.URL.Path, "scheduler",
					"substituted-nonce", http.StatusOK, body))
			},
		},
		{
			name: "status substitution",
			signature: func(request *http.Request) string {
				return hex.EncodeToString(responseMAC(key, request.URL.Path, "scheduler",
					request.Header.Get("X-Robin-Nonce"), http.StatusCreated, body))
			},
		},
		{
			name: "body substitution",
			signature: func(request *http.Request) string {
				return hex.EncodeToString(responseMAC(key, request.URL.Path, "scheduler",
					request.Header.Get("X-Robin-Nonce"), http.StatusOK, []byte(`{}`)))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				w.Header().Set("X-Robin-Response-Signature", test.signature(request))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client, err := NewRunnerClient(server.Client(), server.URL, "scheduler", key)
			if err != nil {
				t.Fatal(err)
			}
			if response, err := client.Run(context.Background(), []byte(`{}`)); err == nil || response != nil {
				t.Fatal("unauthenticated response was accepted")
			}
		})
	}
}

func TestRunnerClientAcceptsOnlyExactSignedResponse(t *testing.T) {
	key := bytes.Repeat([]byte{0x31}, 32)
	body := []byte(`{"kind":"entry"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		signature := responseMAC(key, request.URL.Path, "scheduler", request.Header.Get("X-Robin-Nonce"), http.StatusOK, body)
		w.Header().Set("X-Robin-Response-Signature", hex.EncodeToString(signature))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	client, err := NewRunnerClient(server.Client(), server.URL, "scheduler", key)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Run(context.Background(), []byte(`{}`))
	if err != nil || !bytes.Equal(response, body) {
		t.Fatalf("valid authenticated response rejected: body=%q err=%v", response, err)
	}
}

func TestRunnerClientAuthenticatesErrorResponse(t *testing.T) {
	key := bytes.Repeat([]byte{0x31}, 32)
	body := []byte(`{"error":"ambiguous"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		signature := responseMAC(key, request.URL.Path, "scheduler", request.Header.Get("X-Robin-Nonce"), http.StatusBadGateway, body)
		w.Header().Set("X-Robin-Response-Signature", hex.EncodeToString(signature))
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	client, err := NewRunnerClient(server.Client(), server.URL, "scheduler", key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Run(context.Background(), []byte(`{}`))
	var responseError *ResponseError
	if !errors.As(err, &responseError) || responseError.Status != http.StatusBadGateway ||
		!bytes.Equal(responseError.Body, body) {
		t.Fatalf("signed error response was not preserved: %v", err)
	}
}

func TestRunnerClientRejectsOversizedResponse(t *testing.T) {
	key := bytes.Repeat([]byte{0x31}, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte{'x'}, (256<<10)+1))
	}))
	defer server.Close()
	client, err := NewRunnerClient(server.Client(), server.URL, "scheduler", key)
	if err != nil {
		t.Fatal(err)
	}
	if response, err := client.Run(context.Background(), []byte(`{}`)); err == nil || response != nil {
		t.Fatal("oversized response was accepted")
	}
}

func TestQuoteClientDoesNotRequireRunnerResponseAuthentication(t *testing.T) {
	body := []byte(`{"quote":"signed-by-payload"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()
	client, err := NewQuoteClient(server.Client(), server.URL, "scheduler", bytes.Repeat([]byte{0x31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Quote(context.Background(), []byte(`{}`))
	if err != nil || !bytes.Equal(response, body) {
		t.Fatalf("quote client response changed: body=%q err=%v", response, err)
	}
}
