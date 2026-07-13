package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const maxBodyBytes = 64 << 10

type server struct {
	config  config
	service *service
	store   bindingStore
	now     func() time.Time
	rate    requestRate
}

type requestRate struct {
	mu     sync.Mutex
	window time.Time
	count  uint16
}

type signedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (value *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", value.livez)
	mux.HandleFunc("GET /readyz", value.readyz)
	mux.Handle("POST /v1/graphs/prepare", value.authorize(value.config.APIHMACKey, value.config.CallerID, http.HandlerFunc(value.prepare)))
	mux.Handle("POST /v1/graphs/status", value.authorize(value.config.APIHMACKey, value.config.CallerID, http.HandlerFunc(value.status)))
	mux.Handle("POST /v1/graphs/confirm", value.authorize(value.config.APIHMACKey, value.config.CallerID, http.HandlerFunc(value.confirm)))
	mux.Handle("POST /v1/signer/resolve", value.authorize(value.config.SignerHMACKey, value.config.SignerCallerID, value.signResponse(http.HandlerFunc(value.resolve))))
	return securityHeaders(mux)
}

func (value *server) livez(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "live"})
}

func (value *server) readyz(w http.ResponseWriter, _ *http.Request) {
	if !value.config.Enabled || value.service == nil || value.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "disabled"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (value *server) authorize(key []byte, caller string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !value.config.Enabled || value.service == nil || value.store == nil {
			writeError(w, http.StatusServiceUnavailable, "provisioner disabled")
			return
		}
		if !value.rate.allow(value.now(), value.config.MaxRequestsPerMinute) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, request.Body, maxBodyBytes))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		authorized, err := authorizeRequest(request.Context(), value.store, request, body, key, caller, value.now())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "authorization unavailable")
			return
		}
		if !authorized {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, request)
	})
}

func (value *server) signResponse(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		captured := &signedResponse{header: make(http.Header), status: http.StatusOK}
		next.ServeHTTP(captured, request)
		body := captured.body.Bytes()
		digest := sha256.Sum256(body)
		canonical := fmt.Sprintf("RESPONSE\n%s\n%s\n%s\n%d\n%x", request.URL.Path, value.config.SignerCallerID, request.Header.Get("X-RTC-Nonce"), captured.status, digest)
		mac := hmac.New(sha256.New, value.config.SignerHMACKey)
		_, _ = mac.Write([]byte(canonical))
		for key, values := range captured.header {
			for _, item := range values {
				w.Header().Add(key, item)
			}
		}
		w.Header().Set("X-RTC-Response-Signature", hex.EncodeToString(mac.Sum(nil)))
		w.WriteHeader(captured.status)
		_, _ = w.Write(body)
	})
}

func (value *server) prepare(w http.ResponseWriter, request *http.Request) {
	var body prepareRequest
	if !decodeBody(w, request, &body) {
		return
	}
	result, err := value.service.prepare(request.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (value *server) status(w http.ResponseWriter, request *http.Request) {
	var body statusRequest
	if !decodeBody(w, request, &body) {
		return
	}
	result, err := value.service.status(request.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (value *server) confirm(w http.ResponseWriter, request *http.Request) {
	var body confirmRequest
	if !decodeBody(w, request, &body) {
		return
	}
	result, err := value.service.confirm(request.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (value *server) resolve(w http.ResponseWriter, request *http.Request) {
	var body resolveRequest
	if !decodeBody(w, request, &body) {
		return
	}
	result, err := value.service.resolve(request.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeBody(w http.ResponseWriter, request *http.Request, destination any) bool {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return false
	}
	return true
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidRequest):
		writeError(w, http.StatusBadRequest, "invalid request")
	case errors.Is(err, errConflict):
		writeError(w, http.StatusConflict, "binding conflict")
	case errors.Is(err, errNotReady):
		writeError(w, http.StatusConflict, "binding not ready")
	default:
		writeError(w, http.StatusServiceUnavailable, "provisioning unavailable")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (value *signedResponse) Header() http.Header            { return value.header }
func (value *signedResponse) WriteHeader(status int)         { value.status = status }
func (value *signedResponse) Write(body []byte) (int, error) { return value.body.Write(body) }

func (value *requestRate) allow(now time.Time, limit uint16) bool {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.window.IsZero() || now.Sub(value.window) >= time.Minute {
		value.window = now
		value.count = 0
	}
	if value.count >= limit {
		return false
	}
	value.count++
	return true
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, request)
	})
}
