package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/elliottech/lighter-go/client"
	"github.com/elliottech/lighter-go/types"
	"github.com/elliottech/lighter-go/types/txtypes"
)

const maxBodyBytes = 64 << 10

type signerServer struct {
	config   config
	clients  map[string]*client.TxClient
	accounts map[string]lighterAccountConfig
	now      func() time.Time
	slots    chan struct{}
	rate     requestRate
	nonces   authNonces
}

type requestRate struct {
	mu     sync.Mutex
	window time.Time
	count  uint16
}

type authNonces struct {
	mu      sync.Mutex
	expires map[string]time.Time
}

type signedTransaction struct {
	ExecutionAccountID string          `json:"executionAccountId"`
	AccountIndex       int64           `json:"accountIndex"`
	APIKeyIndex        uint8           `json:"apiKeyIndex"`
	IntentID           string          `json:"intentId,omitempty"`
	TxType             uint8           `json:"txType"`
	TxHash             string          `json:"txHash"`
	TxInfo             json.RawMessage `json:"txInfo"`
}

type transactOptions struct {
	Nonce       int64 `json:"nonce"`
	ExpiresAtMS int64 `json:"expiresAtMs"`
}

type createOrderRequest struct {
	ExecutionAccountID string          `json:"executionAccountId"`
	IntentID           string          `json:"intentId"`
	MarketIndex        int16           `json:"marketIndex"`
	ClientOrderID      int64           `json:"clientOrderIndex"`
	BaseAmount         int64           `json:"baseAmount"`
	Price              uint32          `json:"price"`
	IsAsk              bool            `json:"isAsk"`
	OrderType          uint8           `json:"orderType"`
	TimeInForce        uint8           `json:"timeInForce"`
	ReduceOnly         bool            `json:"reduceOnly"`
	TriggerPrice       uint32          `json:"triggerPrice"`
	OrderExpiryMS      int64           `json:"orderExpiryMs"`
	TransactOptions    transactOptions `json:"transaction"`
}

type modifyOrderRequest struct {
	ExecutionAccountID string          `json:"executionAccountId"`
	IntentID           string          `json:"intentId"`
	MarketIndex        int16           `json:"marketIndex"`
	OrderIndex         int64           `json:"orderIndex"`
	BaseAmount         int64           `json:"baseAmount"`
	Price              uint32          `json:"price"`
	TriggerPrice       uint32          `json:"triggerPrice"`
	TransactOptions    transactOptions `json:"transaction"`
}

type cancelOrderRequest struct {
	ExecutionAccountID string          `json:"executionAccountId"`
	IntentID           string          `json:"intentId"`
	MarketIndex        int16           `json:"marketIndex"`
	OrderIndex         int64           `json:"orderIndex"`
	TransactOptions    transactOptions `json:"transaction"`
}

type cancelAllRequest struct {
	ExecutionAccountID string          `json:"executionAccountId"`
	IntentID           string          `json:"intentId"`
	Mode               string          `json:"mode"`
	ExecuteAtMS        int64           `json:"executeAtMs"`
	TransactOptions    transactOptions `json:"transaction"`
}

type authTokenRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
	ExpiresAtUnix      int64  `json:"expiresAtUnix"`
}

func newSignerServer(value config) (*signerServer, error) {
	server := &signerServer{
		config:   value,
		now:      time.Now,
		nonces:   authNonces{expires: make(map[string]time.Time)},
		clients:  make(map[string]*client.TxClient),
		accounts: make(map[string]lighterAccountConfig),
	}
	if !value.enabled {
		return server, nil
	}
	server.slots = make(chan struct{}, value.maxConcurrentRequests)
	for _, account := range value.accounts {
		txClient, err := client.NewTxClient(
			nil,
			account.PrivateKey,
			account.AccountIndex,
			account.APIKeyIndex,
			account.ChainID,
		)
		if err != nil {
			return nil, fmt.Errorf("initialize Lighter signer for %s: %w", account.ExecutionAccountID, err)
		}
		server.clients[account.ExecutionAccountID] = txClient
		server.accounts[account.ExecutionAccountID] = account
	}
	return server, nil
}

func (s *signerServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", s.livez)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.Handle("POST /v1/sign/create-order", s.authorize(http.HandlerFunc(s.createOrder)))
	mux.Handle("POST /v1/sign/modify-order", s.authorize(http.HandlerFunc(s.modifyOrder)))
	mux.Handle("POST /v1/sign/cancel-order", s.authorize(http.HandlerFunc(s.cancelOrder)))
	mux.Handle("POST /v1/sign/cancel-all", s.authorize(http.HandlerFunc(s.cancelAll)))
	mux.Handle("POST /v1/auth-token", s.authorize(http.HandlerFunc(s.authToken)))
	return securityHeaders(mux)
}

func (s *signerServer) livez(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "live"})
}

func (s *signerServer) readyz(w http.ResponseWriter, _ *http.Request) {
	if !s.config.enabled || len(s.clients) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "disabled"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *signerServer) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.config.enabled || len(s.clients) == 0 {
			writeError(w, http.StatusServiceUnavailable, "signer disabled")
			return
		}
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		default:
			writeError(w, http.StatusTooManyRequests, "signer busy")
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		now := s.now()
		if !s.authorized(r, body, now) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !s.rate.allow(now, s.config.maxRequestsPerMinute) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

func (s *signerServer) authorized(r *http.Request, body []byte, now time.Time) bool {
	caller := r.Header.Get("X-RTC-Caller")
	if subtle.ConstantTimeCompare([]byte(caller), []byte(s.config.callerID)) != 1 {
		return false
	}
	timestampText := r.Header.Get("X-RTC-Timestamp")
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return false
	}
	signedAt := time.Unix(timestamp, 0)
	if signedAt.Before(now.Add(-30*time.Second)) || signedAt.After(now.Add(30*time.Second)) {
		return false
	}
	nonce := r.Header.Get("X-RTC-Nonce")
	if !validAuthNonce(nonce) {
		return false
	}
	provided, err := hex.DecodeString(r.Header.Get("X-RTC-Signature"))
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	bodyDigest := sha256.Sum256(body)
	canonical := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%s\n%x",
		r.Method,
		r.URL.Path,
		caller,
		timestampText,
		nonce,
		bodyDigest,
	)
	mac := hmac.New(sha256.New, s.config.apiHMACKey)
	_, _ = mac.Write([]byte(canonical))
	if subtle.ConstantTimeCompare(mac.Sum(nil), provided) != 1 {
		return false
	}
	return s.nonces.claim(nonce, now, now.Add(time.Minute))
}

func validAuthNonce(value string) bool {
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

func (nonces *authNonces) claim(value string, now, expiresAt time.Time) bool {
	nonces.mu.Lock()
	defer nonces.mu.Unlock()
	for nonce, expiry := range nonces.expires {
		if !expiry.After(now) {
			delete(nonces.expires, nonce)
		}
	}
	if _, exists := nonces.expires[value]; exists {
		return false
	}
	nonces.expires[value] = expiresAt
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (s *signerServer) createOrder(w http.ResponseWriter, r *http.Request) {
	var request createOrderRequest
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	client, account, ok := s.account(request.ExecutionAccountID)
	if request.IntentID == "" || !ok {
		writeError(w, http.StatusBadRequest, "execution account or intent is invalid")
		return
	}
	tx, err := client.GetCreateOrderTransaction(&types.CreateOrderTxReq{
		MarketIndex:      request.MarketIndex,
		ClientOrderIndex: request.ClientOrderID,
		BaseAmount:       request.BaseAmount,
		Price:            request.Price,
		IsAsk:            boolByte(request.IsAsk),
		Type:             request.OrderType,
		TimeInForce:      request.TimeInForce,
		ReduceOnly:       boolByte(request.ReduceOnly),
		TriggerPrice:     request.TriggerPrice,
		OrderExpiry:      request.OrderExpiryMS,
	}, txOptions(request.TransactOptions))
	writeSigned(w, account, request.IntentID, tx, err)
}

func (s *signerServer) modifyOrder(w http.ResponseWriter, r *http.Request) {
	var request modifyOrderRequest
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	client, account, ok := s.account(request.ExecutionAccountID)
	if request.IntentID == "" || !ok {
		writeError(w, http.StatusBadRequest, "execution account or intent is invalid")
		return
	}
	tx, err := client.GetModifyOrderTransaction(&types.ModifyOrderTxReq{
		MarketIndex:  request.MarketIndex,
		Index:        request.OrderIndex,
		BaseAmount:   request.BaseAmount,
		Price:        request.Price,
		TriggerPrice: request.TriggerPrice,
	}, txOptions(request.TransactOptions))
	writeSigned(w, account, request.IntentID, tx, err)
}

func (s *signerServer) cancelOrder(w http.ResponseWriter, r *http.Request) {
	var request cancelOrderRequest
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	client, account, ok := s.account(request.ExecutionAccountID)
	if request.IntentID == "" || !ok {
		writeError(w, http.StatusBadRequest, "execution account or intent is invalid")
		return
	}
	tx, err := client.GetCancelOrderTransaction(&types.CancelOrderTxReq{
		MarketIndex: request.MarketIndex,
		Index:       request.OrderIndex,
	}, txOptions(request.TransactOptions))
	writeSigned(w, account, request.IntentID, tx, err)
}

func (s *signerServer) cancelAll(w http.ResponseWriter, r *http.Request) {
	var request cancelAllRequest
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	client, account, ok := s.account(request.ExecutionAccountID)
	if request.IntentID == "" || !ok {
		writeError(w, http.StatusBadRequest, "execution account or intent is invalid")
		return
	}
	var mode uint8
	switch request.Mode {
	case "immediate":
		mode = txtypes.ImmediateCancelAll
		request.ExecuteAtMS = 0
	case "scheduled":
		mode = txtypes.ScheduledCancelAll
	case "abort_scheduled":
		mode = txtypes.AbortScheduledCancelAll
		request.ExecuteAtMS = 0
	default:
		writeError(w, http.StatusBadRequest, "invalid cancel-all mode")
		return
	}
	tx, err := client.GetCancelAllOrdersTransaction(&types.CancelAllOrdersTxReq{
		TimeInForce: mode,
		Time:        request.ExecuteAtMS,
	}, txOptions(request.TransactOptions))
	writeSigned(w, account, request.IntentID, tx, err)
}

func (s *signerServer) authToken(w http.ResponseWriter, r *http.Request) {
	var request authTokenRequest
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	client, account, ok := s.account(request.ExecutionAccountID)
	if !ok {
		writeError(w, http.StatusBadRequest, "execution account is invalid")
		return
	}
	now := time.Now().Unix()
	if request.ExpiresAtUnix <= now || request.ExpiresAtUnix > now+8*60*60 {
		writeError(w, http.StatusBadRequest, "expiry must be within eight hours")
		return
	}
	token, err := client.GetAuthToken(time.Unix(request.ExpiresAtUnix, 0))
	if err != nil {
		writeError(w, http.StatusBadRequest, "unable to create auth token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"executionAccountId": account.ExecutionAccountID,
		"token":              token,
		"expiresAtUnix":      request.ExpiresAtUnix,
	})
}

func txOptions(value transactOptions) *types.TransactOpts {
	nonce := value.Nonce
	return &types.TransactOpts{Nonce: &nonce, ExpiredAt: value.ExpiresAtMS}
}

func writeSigned(w http.ResponseWriter, account lighterAccountConfig, intentID string, tx txtypes.TxInfo, err error) {
	if err != nil {
		writeError(w, http.StatusBadRequest, "transaction declined")
		return
	}
	info, err := tx.GetTxInfo()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "transaction encoding failed")
		return
	}
	writeJSON(w, http.StatusOK, signedTransaction{
		ExecutionAccountID: account.ExecutionAccountID,
		AccountIndex:       account.AccountIndex,
		APIKeyIndex:        account.APIKeyIndex,
		IntentID:           intentID,
		TxType:             tx.GetTxType(),
		TxHash:             tx.GetTxHash(),
		TxInfo:             json.RawMessage(info),
	})
}

func (s *signerServer) account(executionAccountID string) (*client.TxClient, lighterAccountConfig, bool) {
	client, exists := s.clients[executionAccountID]
	account, configured := s.accounts[executionAccountID]
	return client, account, exists && configured
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) error {
	reader := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request must contain one JSON value")
		return fmt.Errorf("trailing request data")
	}
	return nil
}

func boolByte(value bool) uint8 {
	if value {
		return 1
	}
	return 0
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
