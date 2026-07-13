package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

const maxBodyBytes = 64 << 10

type server struct {
	config  config
	service *service
	store   credentialStore
	now     func() time.Time
}

func (value *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", value.livez)
	mux.HandleFunc("GET /readyz", value.readyz)
	mux.Handle("POST /v1/links/prepare", value.authorize(http.HandlerFunc(value.prepare)))
	mux.Handle("POST /v1/links/status", value.authorize(http.HandlerFunc(value.status)))
	mux.Handle("POST /v1/links/confirm", value.authorize(http.HandlerFunc(value.confirm)))
	mux.Handle("POST /v1/signer/auth-token", value.authorize(http.HandlerFunc(value.authToken)))
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

func (value *server) authorize(next http.Handler) http.Handler {
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
		authorized, err := authorizeRequest(value.store, request, body, value.config.HMACKey, value.config.CallerID, value.now())
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

func (value *server) authToken(w http.ResponseWriter, request *http.Request) {
	var body authTokenRequest
	if err := decodeBody(w, request, &body); err != nil {
		return
	}
	result, err := value.service.authToken(request.Context(), body)
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
	case errors.Is(err, errBindingMismatch), errors.Is(err, errRotationOpen):
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
