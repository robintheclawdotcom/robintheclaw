package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/elliottech/lighter-go/client"
	"github.com/elliottech/lighter-go/types"
	"github.com/elliottech/lighter-go/types/txtypes"
)

const maxBodyBytes = 64 << 10

type signerServer struct {
	config config
	client *client.TxClient
}

type signedTransaction struct {
	IntentID string          `json:"intentId,omitempty"`
	TxType   uint8           `json:"txType"`
	TxHash   string          `json:"txHash"`
	TxInfo   json.RawMessage `json:"txInfo"`
}

type transactOptions struct {
	Nonce       int64 `json:"nonce"`
	ExpiresAtMS int64 `json:"expiresAtMs"`
}

type createOrderRequest struct {
	IntentID        string          `json:"intentId"`
	MarketIndex     int16           `json:"marketIndex"`
	ClientOrderID   int64           `json:"clientOrderIndex"`
	BaseAmount      int64           `json:"baseAmount"`
	Price           uint32          `json:"price"`
	IsAsk           bool            `json:"isAsk"`
	OrderType       uint8           `json:"orderType"`
	TimeInForce     uint8           `json:"timeInForce"`
	ReduceOnly      bool            `json:"reduceOnly"`
	TriggerPrice    uint32          `json:"triggerPrice"`
	OrderExpiryMS   int64           `json:"orderExpiryMs"`
	TransactOptions transactOptions `json:"transaction"`
}

type modifyOrderRequest struct {
	IntentID        string          `json:"intentId"`
	MarketIndex     int16           `json:"marketIndex"`
	OrderIndex      int64           `json:"orderIndex"`
	BaseAmount      int64           `json:"baseAmount"`
	Price           uint32          `json:"price"`
	TriggerPrice    uint32          `json:"triggerPrice"`
	TransactOptions transactOptions `json:"transaction"`
}

type cancelOrderRequest struct {
	IntentID        string          `json:"intentId"`
	MarketIndex     int16           `json:"marketIndex"`
	OrderIndex      int64           `json:"orderIndex"`
	TransactOptions transactOptions `json:"transaction"`
}

type cancelAllRequest struct {
	IntentID        string          `json:"intentId"`
	Mode            string          `json:"mode"`
	ExecuteAtMS     int64           `json:"executeAtMs"`
	TransactOptions transactOptions `json:"transaction"`
}

type authTokenRequest struct {
	ExpiresAtUnix int64 `json:"expiresAtUnix"`
}

func newSignerServer(value config) (*signerServer, error) {
	server := &signerServer{config: value}
	if !value.enabled {
		return server, nil
	}
	txClient, err := client.NewTxClient(
		nil,
		value.privateKey,
		value.accountIndex,
		value.apiKeyIndex,
		value.chainID,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize Lighter signer: %w", err)
	}
	server.client = txClient
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
	return mux
}

func (s *signerServer) livez(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "live"})
}

func (s *signerServer) readyz(w http.ResponseWriter, _ *http.Request) {
	if !s.config.enabled || s.client == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "disabled"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *signerServer) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.config.enabled || s.client == nil {
			writeError(w, http.StatusServiceUnavailable, "signer disabled")
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(provided) != len(s.config.serviceToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.config.serviceToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *signerServer) createOrder(w http.ResponseWriter, r *http.Request) {
	var request createOrderRequest
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	if request.IntentID == "" {
		writeError(w, http.StatusBadRequest, "intentId is required")
		return
	}
	tx, err := s.client.GetCreateOrderTransaction(&types.CreateOrderTxReq{
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
	writeSigned(w, request.IntentID, tx, err)
}

func (s *signerServer) modifyOrder(w http.ResponseWriter, r *http.Request) {
	var request modifyOrderRequest
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	if request.IntentID == "" {
		writeError(w, http.StatusBadRequest, "intentId is required")
		return
	}
	tx, err := s.client.GetModifyOrderTransaction(&types.ModifyOrderTxReq{
		MarketIndex:  request.MarketIndex,
		Index:        request.OrderIndex,
		BaseAmount:   request.BaseAmount,
		Price:        request.Price,
		TriggerPrice: request.TriggerPrice,
	}, txOptions(request.TransactOptions))
	writeSigned(w, request.IntentID, tx, err)
}

func (s *signerServer) cancelOrder(w http.ResponseWriter, r *http.Request) {
	var request cancelOrderRequest
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	if request.IntentID == "" {
		writeError(w, http.StatusBadRequest, "intentId is required")
		return
	}
	tx, err := s.client.GetCancelOrderTransaction(&types.CancelOrderTxReq{
		MarketIndex: request.MarketIndex,
		Index:       request.OrderIndex,
	}, txOptions(request.TransactOptions))
	writeSigned(w, request.IntentID, tx, err)
}

func (s *signerServer) cancelAll(w http.ResponseWriter, r *http.Request) {
	var request cancelAllRequest
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	if request.IntentID == "" {
		writeError(w, http.StatusBadRequest, "intentId is required")
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
	tx, err := s.client.GetCancelAllOrdersTransaction(&types.CancelAllOrdersTxReq{
		TimeInForce: mode,
		Time:        request.ExecuteAtMS,
	}, txOptions(request.TransactOptions))
	writeSigned(w, request.IntentID, tx, err)
}

func (s *signerServer) authToken(w http.ResponseWriter, r *http.Request) {
	var request authTokenRequest
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	now := time.Now().Unix()
	if request.ExpiresAtUnix <= now || request.ExpiresAtUnix > now+8*60*60 {
		writeError(w, http.StatusBadRequest, "expiry must be within eight hours")
		return
	}
	token, err := s.client.GetAuthToken(time.Unix(request.ExpiresAtUnix, 0))
	if err != nil {
		writeError(w, http.StatusBadRequest, "unable to create auth token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":         token,
		"expiresAtUnix": request.ExpiresAtUnix,
	})
}

func txOptions(value transactOptions) *types.TransactOpts {
	nonce := value.Nonce
	return &types.TransactOpts{Nonce: &nonce, ExpiredAt: value.ExpiresAtMS}
}

func writeSigned(w http.ResponseWriter, intentID string, tx txtypes.TxInfo, err error) {
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
		IntentID: intentID,
		TxType:   tx.GetTxType(),
		TxHash:   tx.GetTxHash(),
		TxInfo:   json.RawMessage(info),
	})
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
