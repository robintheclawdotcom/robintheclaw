package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDisabledSignerFailsClosed(t *testing.T) {
	server, err := newSignerServer(config{})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/sign/create-order", strings.NewReader("{}"))
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response = httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz got status %d", response.Code)
	}
}

func TestLivezDoesNotClaimReadiness(t *testing.T) {
	server, err := newSignerServer(config{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/livez", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("livez got status %d", response.Code)
	}
}

func TestSignerSurfaceHasNoAssetMovementRoute(t *testing.T) {
	server, err := newSignerServer(config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/sign/withdraw", "/v1/sign/transfer", "/v1/sign/update-leverage"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		response := httptest.NewRecorder()
		server.routes().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s got status %d", path, response.Code)
		}
	}
}
