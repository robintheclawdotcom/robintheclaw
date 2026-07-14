package quoteauthority

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/robin-the-claw/liveexec/protocol"
)

const maximumBodyBytes = 64 << 10

type Server struct {
	service    *Service
	entryAuth  *protocol.Authenticator
	exitAuth   *protocol.Authenticator
	enabled    bool
	sharedAuth bool
}

func NewServer(service *Service, auth *protocol.Authenticator, enabled bool) *Server {
	return &Server{service: service, entryAuth: auth, exitAuth: auth, enabled: enabled, sharedAuth: true}
}

func NewDualAuthServer(service *Service, entryAuth, exitAuth *protocol.Authenticator, enabled bool) *Server {
	return &Server{service: service, entryAuth: entryAuth, exitAuth: exitAuth, enabled: enabled}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /v1/executable-quotes", s.executableQuotes)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	status := http.StatusOK
	state := "disabled"
	if s.enabled && s.service != nil && s.entryAuth != nil && s.exitAuth != nil {
		state = "ready"
	} else if s.enabled {
		status = http.StatusServiceUnavailable
		state = "blocked"
	}
	writeJSON(w, status, map[string]string{"status": state})
}

func (s *Server) executableQuotes(w http.ResponseWriter, request *http.Request) {
	if !s.enabled || s.service == nil || s.entryAuth == nil || s.exitAuth == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "quote authority is disabled"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, request.Body, maximumBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	verify := func(auth *protocol.Authenticator) bool {
		return auth.Verify(request.Method, request.URL.Path, request.Header.Get("X-Robin-Caller"),
			request.Header.Get("X-Robin-Timestamp"), request.Header.Get("X-Robin-Nonce"),
			request.Header.Get("X-Robin-Signature"), body) == nil
	}
	entryAuthorized := verify(s.entryAuth)
	exitAuthorized := entryAuthorized
	if !s.sharedAuth {
		exitAuthorized = verify(s.exitAuth)
	}
	if !entryAuthorized && !exitAuthorized {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "request authentication failed"})
		return
	}
	var input protocol.QuoteRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid quote request"})
		return
	}
	if (!s.sharedAuth && input.Action == protocol.ActionEntry && !entryAuthorized) ||
		(!s.sharedAuth && input.Action == protocol.ActionUnwind && !exitAuthorized) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "request authentication failed"})
		return
	}
	quote, err := s.service.Quote(request.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "executable quote unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, quote)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
