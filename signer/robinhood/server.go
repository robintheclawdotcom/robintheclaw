package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type Server struct {
	config  Config
	writer  *Writer
	writers map[string]*Writer
	manager *accountWriterManager
	once    sync.Once
	slots   chan struct{}
	rate    requestRate
}

type requestRate struct {
	mu     sync.Mutex
	window time.Time
	count  uint16
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", server.live)
	mux.HandleFunc("GET /readyz", server.ready)
	mux.HandleFunc("POST /v1/spot-intents", server.executeSpot)
	return signedResponses(securityHeaders(mux), server.config.CallerID, server.config.APIHMACKey)
}

func (server *Server) live(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "live"})
}

func (server *Server) ready(response http.ResponseWriter, request *http.Request) {
	if !server.config.Enabled || !server.anyWriterReady(request.Context()) {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{"status": "unready"})
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ready"})
}

func (server *Server) executeSpot(response http.ResponseWriter, request *http.Request) {
	if !server.config.Enabled || (server.writer == nil && len(server.writers) == 0 && server.manager == nil) {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "signer unavailable"})
		return
	}
	server.once.Do(func() {
		server.slots = make(chan struct{}, server.config.MaxConcurrentRequests)
	})
	select {
	case server.slots <- struct{}{}:
		defer func() { <-server.slots }()
	default:
		writeJSON(response, http.StatusTooManyRequests, map[string]string{"error": "signer busy"})
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 16<<10)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), server.config.RequestTimeout)
	defer cancel()
	request = request.WithContext(ctx)
	if !server.authorized(request, body) {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !server.rate.allow(time.Now(), server.config.MaxRequestsPerMinute) {
		writeJSON(response, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var payload ExecuteRequest
	if err := decoder.Decode(&payload); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	writer, err := server.writerFor(request.Context(), payload.ExecutionAccountID)
	if err != nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "execution account resolution unavailable"})
		return
	}
	if writer == nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "execution account is not registered"})
		return
	}
	submission, err := writer.Submit(ctx, payload)
	if err != nil {
		var tracked *journaledSubmissionError
		if errors.As(err, &tracked) {
			slog.Warn(
				"spot intent requires reconciliation",
				"request_id", payload.RequestID,
				"status", tracked.Submission().Status,
				"error", err,
			)
			writeJSON(response, http.StatusAccepted, tracked.Submission())
			return
		}
		status := http.StatusConflict
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		} else if errors.Is(err, errWriterNotReady) {
			status = http.StatusServiceUnavailable
		}
		slog.Warn("spot intent rejected", "request_id", payload.RequestID, "error", err)
		writeJSON(response, status, map[string]string{"error": "request rejected"})
		return
	}
	writeJSON(response, http.StatusAccepted, submission)
}

func (server *Server) authorized(request *http.Request, body []byte) bool {
	caller := request.Header.Get("X-RTC-Caller")
	if subtle.ConstantTimeCompare([]byte(caller), []byte(server.config.CallerID)) != 1 {
		return false
	}
	timestampText := request.Header.Get("X-RTC-Timestamp")
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return false
	}
	now := time.Now()
	signedAt := time.Unix(timestamp, 0)
	if signedAt.Before(now.Add(-30*time.Second)) || signedAt.After(now.Add(30*time.Second)) {
		return false
	}
	nonce := request.Header.Get("X-RTC-Nonce")
	if !validNonce(nonce) {
		return false
	}
	provided, err := hex.DecodeString(request.Header.Get("X-RTC-Signature"))
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	bodyDigest := sha256.Sum256(body)
	canonical := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%s\n%x",
		request.Method,
		request.URL.Path,
		caller,
		timestampText,
		nonce,
		bodyDigest,
	)
	mac := hmac.New(sha256.New, server.config.APIHMACKey)
	_, _ = mac.Write([]byte(canonical))
	if subtle.ConstantTimeCompare(mac.Sum(nil), provided) != 1 {
		return false
	}
	expiresAt := signedAt.Add(time.Minute)
	var binding struct {
		ExecutionAccountID string `json:"execution_account_id"`
	}
	if json.Unmarshal(body, &binding) != nil {
		return false
	}
	writer, err := server.writerFor(request.Context(), binding.ExecutionAccountID)
	if err != nil || writer == nil {
		return false
	}
	return writer.journal.ClaimAuthNonce(request.Context(), nonce, expiresAt) == nil
}

func (server *Server) writerFor(ctx context.Context, executionAccountID string) (*Writer, error) {
	if server.manager != nil {
		return server.manager.writer(ctx, executionAccountID)
	}
	if writer := server.writers[executionAccountID]; writer != nil {
		return writer, nil
	}
	if server.writer != nil && executionAccountID == server.config.ExecutionAccountID {
		return server.writer, nil
	}
	return nil, nil
}

func (server *Server) anyWriterReady(ctx context.Context) bool {
	if server.manager != nil {
		return server.manager.ready(ctx)
	}
	configured := 0
	if server.writer != nil {
		configured++
		if !server.writer.Ready() {
			return false
		}
	}
	for _, writer := range server.writers {
		configured++
		if !writer.Ready() {
			return false
		}
	}
	return configured > 0
}

func validNonce(value string) bool {
	if len(value) < 32 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func (rate *requestRate) allow(now time.Time, limit uint16) bool {
	rate.mu.Lock()
	defer rate.mu.Unlock()
	if rate.window.IsZero() || now.Sub(rate.window) >= time.Minute {
		rate.window = now
		rate.count = 0
	}
	if rate.count >= limit {
		return false
	}
	rate.count++
	return true
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(response, request)
	})
}

type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (response *bufferedResponse) Header() http.Header {
	return response.header
}

func (response *bufferedResponse) WriteHeader(status int) {
	if response.status == 0 {
		response.status = status
	}
}

func (response *bufferedResponse) Write(body []byte) (int, error) {
	if response.status == 0 {
		response.status = http.StatusOK
	}
	return response.body.Write(body)
}

func signedResponses(next http.Handler, caller string, key []byte) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		buffered := &bufferedResponse{header: make(http.Header)}
		next.ServeHTTP(buffered, request)
		if buffered.status == 0 {
			buffered.status = http.StatusOK
		}
		for name, values := range buffered.header {
			response.Header()[name] = append([]string(nil), values...)
		}
		response.Header().Set(
			"X-RTC-Response-Signature",
			signResponse(
				key,
				request.URL.Path,
				caller,
				request.Header.Get("X-RTC-Nonce"),
				buffered.status,
				buffered.body.Bytes(),
			),
		)
		response.WriteHeader(buffered.status)
		_, _ = response.Write(buffered.body.Bytes())
	})
}

func signResponse(key []byte, path, caller, nonce string, status int, body []byte) string {
	digest := sha256.Sum256(body)
	canonical := fmt.Sprintf("RESPONSE\n%s\n%s\n%s\n%d\n%x", path, caller, nonce, status, digest)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func httpServer(config Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              config.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}
