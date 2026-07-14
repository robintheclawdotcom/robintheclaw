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
	config         config
	service        *service
	store          credentialStore
	now            func() time.Time
	signingSlots   chan struct{}
	signingRate    provisionerRate
	publisherSlots chan struct{}
	publisherRate  provisionerRate
}

type provisionerRate struct {
	mu     sync.Mutex
	window time.Time
	count  uint16
}

type signingResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newSigningResponse() *signingResponse {
	return &signingResponse{header: make(http.Header), status: http.StatusOK}
}

func (value *signingResponse) Header() http.Header { return value.header }

func (value *signingResponse) WriteHeader(status int) { value.status = status }

func (value *signingResponse) Write(body []byte) (int, error) { return value.body.Write(body) }

func (value *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", value.livez)
	mux.HandleFunc("GET /readyz", value.readyz)
	mux.Handle("POST /v1/links/prepare", value.authorize(http.HandlerFunc(value.prepare)))
	mux.Handle("POST /v1/links/status", value.authorize(http.HandlerFunc(value.status)))
	mux.Handle("POST /v1/links/confirm", value.authorize(http.HandlerFunc(value.confirm)))
	mux.Handle("POST /v1/signer/create-order", value.authorizeSigner(http.HandlerFunc(value.createOrder)))
	mux.Handle("POST /v1/signer/cancel-order", value.authorizeSigner(http.HandlerFunc(value.cancelOrder)))
	mux.Handle("POST /v1/signer/cancel-all", value.authorizeSigner(http.HandlerFunc(value.cancelAll)))
	mux.Handle("POST /v1/publisher/account-state", value.authorizePublisher(http.HandlerFunc(value.publisherAccountState)))
	return securityHeaders(mux)
}

func (value *server) authorizeSigner(next http.Handler) http.Handler {
	return value.authorizePrivate(
		next, value.config.SignerHMACKey, value.config.SignerCallerID,
		value.signingSlots, &value.signingRate, value.config.SigningMaxRequestsPerMinute, "signing",
	)
}

func (value *server) authorizePublisher(next http.Handler) http.Handler {
	return value.authorizePrivate(
		next, value.config.PublisherHMACKey, value.config.PublisherCallerID,
		value.publisherSlots, &value.publisherRate, value.config.PublisherMaxRequestsPerMinute, "publisher",
	)
}

func (value *server) authorizePrivate(next http.Handler, key []byte, caller string, slots chan struct{}, rate *provisionerRate, limit uint16, label string) http.Handler {
	limited := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if slots == nil {
			writeError(w, http.StatusServiceUnavailable, label+" bridge disabled")
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			writeError(w, http.StatusTooManyRequests, label+" bridge busy")
			return
		}
		if !rate.allow(value.now(), limit) {
			writeError(w, http.StatusTooManyRequests, label+" rate limit exceeded")
			return
		}
		next.ServeHTTP(w, request)
	})
	signed := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		response := newSigningResponse()
		limited.ServeHTTP(response, request)
		body := response.body.Bytes()
		digest := sha256.Sum256(body)
		canonical := fmt.Sprintf(
			"RESPONSE\n%s\n%s\n%s\n%d\n%x",
			request.URL.Path,
			caller,
			request.Header.Get("X-RTC-Nonce"),
			response.status,
			digest,
		)
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(canonical))
		for key, values := range response.header {
			for _, item := range values {
				w.Header().Add(key, item)
			}
		}
		w.Header().Set("X-RTC-Response-Signature", hex.EncodeToString(mac.Sum(nil)))
		w.WriteHeader(response.status)
		_, _ = w.Write(body)
	})
	return value.authorizeWith(key, caller, signed)
}

func (value *server) authorizeWith(key []byte, caller string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !value.config.Enabled || value.service == nil || value.store == nil {
			writeError(w, http.StatusServiceUnavailable, "provisioner disabled")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, request.Body, maxBodyBytes))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		authorized, err := authorizeRequest(value.store, request, body, key, caller, value.now())
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

func (value *server) authorize(next http.Handler) http.Handler {
	return value.authorizeWith(value.config.HMACKey, value.config.CallerID, next)
}

func (value *provisionerRate) allow(now time.Time, limit uint16) bool {
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

func (value *server) prepare(w http.ResponseWriter, request *http.Request) {
	var body prepareRequest
	if err := decodeBody(w, request, &body); err != nil {
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
	if err := decodeBody(w, request, &body); err != nil {
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
	if err := decodeBody(w, request, &body); err != nil {
		return
	}
	result, linked, err := value.service.confirm(request.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	status := http.StatusAccepted
	if linked {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

func (value *server) publisherAccountState(w http.ResponseWriter, request *http.Request) {
	var body publisherAccountStateRequest
	if err := decodeBody(w, request, &body); err != nil {
		return
	}
	result, err := value.service.publisherAccountState(request.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (value *server) createOrder(w http.ResponseWriter, request *http.Request) {
	var body createOrderRequest
	if err := decodeBody(w, request, &body); err != nil {
		return
	}
	result, err := value.service.signCreateOrder(request.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (value *server) cancelOrder(w http.ResponseWriter, request *http.Request) {
	var body cancelOrderRequest
	if err := decodeBody(w, request, &body); err != nil {
		return
	}
	result, err := value.service.signCancelOrder(request.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (value *server) cancelAll(w http.ResponseWriter, request *http.Request) {
	var body cancelAllRequest
	if err := decodeBody(w, request, &body); err != nil {
		return
	}
	result, err := value.service.signCancelAll(request.Context(), body)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeBody(w http.ResponseWriter, request *http.Request, target any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotFound):
		writeError(w, http.StatusNotFound, "link not found")
	case errors.Is(err, errObservationRateLimited):
		writeError(w, http.StatusTooManyRequests, "Lighter observation rate limited")
	case errors.Is(err, errBindingMismatch), errors.Is(err, errRotationOpen), errors.Is(err, errAccountBound):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
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

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, request)
	})
}
