package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/elliottech/lighter-go/client"
	"github.com/elliottech/lighter-go/types"
	"github.com/elliottech/lighter-go/types/txtypes"
)

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

type signedTransaction struct {
	ExecutionAccountID string          `json:"executionAccountId"`
	AccountIndex       int64           `json:"accountIndex"`
	APIKeyIndex        uint8           `json:"apiKeyIndex"`
	CredentialVersion  int64           `json:"credentialVersion"`
	IntentID           string          `json:"intentId"`
	TxType             uint8           `json:"txType"`
	TxHash             string          `json:"txHash"`
	TxInfo             json.RawMessage `json:"txInfo"`
}

func (value *service) signCreateOrder(ctx context.Context, request createOrderRequest) (signedTransaction, error) {
	if err := validateSigningRequest(request.ExecutionAccountID, request.IntentID, request.TransactOptions, value.now()); err != nil {
		return signedTransaction{}, err
	}
	return value.sign(ctx, request.ExecutionAccountID, request.IntentID, request.TransactOptions, func(secret string, record credential) (signedTransaction, error) {
		return value.lighter.SignCreateOrder(secret, record.AccountIndex, record.APIKeyIndex, request)
	})
}

func (value *service) signModifyOrder(ctx context.Context, request modifyOrderRequest) (signedTransaction, error) {
	if err := validateSigningRequest(request.ExecutionAccountID, request.IntentID, request.TransactOptions, value.now()); err != nil {
		return signedTransaction{}, err
	}
	return value.sign(ctx, request.ExecutionAccountID, request.IntentID, request.TransactOptions, func(secret string, record credential) (signedTransaction, error) {
		return value.lighter.SignModifyOrder(secret, record.AccountIndex, record.APIKeyIndex, request)
	})
}

func (value *service) signCancelOrder(ctx context.Context, request cancelOrderRequest) (signedTransaction, error) {
	if err := validateSigningRequest(request.ExecutionAccountID, request.IntentID, request.TransactOptions, value.now()); err != nil {
		return signedTransaction{}, err
	}
	return value.sign(ctx, request.ExecutionAccountID, request.IntentID, request.TransactOptions, func(secret string, record credential) (signedTransaction, error) {
		return value.lighter.SignCancelOrder(secret, record.AccountIndex, record.APIKeyIndex, request)
	})
}

func (value *service) signCancelAll(ctx context.Context, request cancelAllRequest) (signedTransaction, error) {
	if err := validateSigningRequest(request.ExecutionAccountID, request.IntentID, request.TransactOptions, value.now()); err != nil {
		return signedTransaction{}, err
	}
	if request.Mode != "immediate" && request.Mode != "scheduled" && request.Mode != "abort_scheduled" {
		return signedTransaction{}, errors.New("invalid cancel-all mode")
	}
	return value.sign(ctx, request.ExecutionAccountID, request.IntentID, request.TransactOptions, func(secret string, record credential) (signedTransaction, error) {
		return value.lighter.SignCancelAll(secret, record.AccountIndex, record.APIKeyIndex, request)
	})
}

func (value *service) sign(
	ctx context.Context,
	executionID string,
	intentID string,
	options transactOptions,
	build func(string, credential) (signedTransaction, error),
) (signedTransaction, error) {
	record, secretBytes, err := value.activeSecret(ctx, executionID)
	if err != nil {
		return signedTransaction{}, err
	}
	defer zero(secretBytes)
	result, err := build(transientString(secretBytes), record)
	if err != nil {
		return signedTransaction{}, errors.New("transaction declined")
	}
	result.ExecutionAccountID = record.ExecutionAccountID
	result.AccountIndex = record.AccountIndex
	result.APIKeyIndex = record.APIKeyIndex
	result.CredentialVersion = record.Version
	result.IntentID = intentID
	if err := validateSignedIdentity(result); err != nil {
		_ = value.store.Block(context.WithoutCancel(ctx), record, "signed_transaction_identity_mismatch")
		return signedTransaction{}, err
	}
	if err := value.store.AuditActive(ctx, record, "transaction_signed", map[string]any{
		"intentId":        intentID,
		"nonce":           options.Nonce,
		"transactionHash": result.TxHash,
		"transactionType": result.TxType,
	}); err != nil {
		return signedTransaction{}, err
	}
	return result, nil
}

func (value *service) activeSecret(ctx context.Context, executionID string) (credential, []byte, error) {
	executionID = strings.ToLower(executionID)
	record, err := value.store.Active(ctx, executionID)
	if err != nil {
		return credential{}, nil, errors.New("execution account has no active Lighter credential")
	}
	if record.ExecutionAccountID != executionID || record.AccountIndex <= 0 || record.APIKeyIndex < 2 || record.APIKeyIndex > 254 {
		return credential{}, nil, errors.New("active Lighter credential identity mismatch")
	}
	registered, err := value.lighter.RegisteredPublicKey(record.AccountIndex, record.APIKeyIndex)
	if err != nil {
		return credential{}, nil, errors.New("verify active Lighter credential")
	}
	if normalizePublicKey(registered) != normalizePublicKey(record.PublicKey) {
		_ = value.store.Block(context.WithoutCancel(ctx), record, "active_public_key_mismatch")
		return credential{}, nil, errors.New("active Lighter credential no longer matches registered key")
	}
	secret, err := value.envelope.open(ctx, record)
	if err != nil {
		_ = value.store.Block(context.WithoutCancel(ctx), record, "decrypt_failed")
		return credential{}, nil, err
	}
	return record, secret, nil
}

func validateSigningRequest(executionID, intentID string, options transactOptions, now time.Time) error {
	if err := validateExecutionAccountID(executionID); err != nil {
		return err
	}
	if !validIntentID(intentID) {
		return errors.New("intentId is invalid")
	}
	if options.Nonce < 0 || options.ExpiresAtMS <= now.UnixMilli() || options.ExpiresAtMS > now.Add(10*time.Minute).UnixMilli() {
		return errors.New("transaction nonce or expiry is invalid")
	}
	return nil
}

func validIntentID(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' && character != ':' {
			return false
		}
	}
	return true
}

func validateSignedIdentity(value signedTransaction) error {
	if value.TxType == 0 || value.TxHash == "" || !json.Valid(value.TxInfo) {
		return errors.New("signed transaction is invalid")
	}
	var identity struct {
		AccountIndex int64 `json:"AccountIndex"`
		APIKeyIndex  uint8 `json:"ApiKeyIndex"`
	}
	if err := json.Unmarshal(value.TxInfo, &identity); err != nil || identity.AccountIndex != value.AccountIndex || identity.APIKeyIndex != value.APIKeyIndex {
		return errors.New("signed transaction identity mismatch")
	}
	return nil
}

func (value *liveLighterClient) transactionClient(secret string, accountIndex int64, apiKeyIndex uint8) (*client.TxClient, error) {
	result, err := client.NewTxClient(nil, secret, accountIndex, apiKeyIndex, value.chainID)
	if err != nil {
		return nil, errors.New("initialize Lighter transaction client")
	}
	return result, nil
}

func (value *liveLighterClient) SignCreateOrder(secret string, accountIndex int64, apiKeyIndex uint8, request createOrderRequest) (signedTransaction, error) {
	client, err := value.transactionClient(secret, accountIndex, apiKeyIndex)
	if err != nil {
		return signedTransaction{}, err
	}
	tx, err := client.GetCreateOrderTransaction(&types.CreateOrderTxReq{
		MarketIndex: request.MarketIndex, ClientOrderIndex: request.ClientOrderID,
		BaseAmount: request.BaseAmount, Price: request.Price, IsAsk: boolByte(request.IsAsk),
		Type: request.OrderType, TimeInForce: request.TimeInForce, ReduceOnly: boolByte(request.ReduceOnly),
		TriggerPrice: request.TriggerPrice, OrderExpiry: request.OrderExpiryMS,
	}, txOptions(request.TransactOptions))
	return encodeTransaction(tx, err)
}

func (value *liveLighterClient) SignModifyOrder(secret string, accountIndex int64, apiKeyIndex uint8, request modifyOrderRequest) (signedTransaction, error) {
	client, err := value.transactionClient(secret, accountIndex, apiKeyIndex)
	if err != nil {
		return signedTransaction{}, err
	}
	tx, err := client.GetModifyOrderTransaction(&types.ModifyOrderTxReq{
		MarketIndex: request.MarketIndex, Index: request.OrderIndex, BaseAmount: request.BaseAmount,
		Price: request.Price, TriggerPrice: request.TriggerPrice,
	}, txOptions(request.TransactOptions))
	return encodeTransaction(tx, err)
}

func (value *liveLighterClient) SignCancelOrder(secret string, accountIndex int64, apiKeyIndex uint8, request cancelOrderRequest) (signedTransaction, error) {
	client, err := value.transactionClient(secret, accountIndex, apiKeyIndex)
	if err != nil {
		return signedTransaction{}, err
	}
	tx, err := client.GetCancelOrderTransaction(&types.CancelOrderTxReq{
		MarketIndex: request.MarketIndex, Index: request.OrderIndex,
	}, txOptions(request.TransactOptions))
	return encodeTransaction(tx, err)
}

func (value *liveLighterClient) SignCancelAll(secret string, accountIndex int64, apiKeyIndex uint8, request cancelAllRequest) (signedTransaction, error) {
	client, err := value.transactionClient(secret, accountIndex, apiKeyIndex)
	if err != nil {
		return signedTransaction{}, err
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
		return signedTransaction{}, errors.New("invalid cancel-all mode")
	}
	tx, err := client.GetCancelAllOrdersTransaction(&types.CancelAllOrdersTxReq{
		TimeInForce: mode, Time: request.ExecuteAtMS,
	}, txOptions(request.TransactOptions))
	return encodeTransaction(tx, err)
}

func txOptions(value transactOptions) *types.TransactOpts {
	nonce := value.Nonce
	return &types.TransactOpts{Nonce: &nonce, ExpiredAt: value.ExpiresAtMS}
}

func encodeTransaction(tx txtypes.TxInfo, err error) (signedTransaction, error) {
	if err != nil {
		return signedTransaction{}, err
	}
	info, err := tx.GetTxInfo()
	if err != nil {
		return signedTransaction{}, errors.New("encode Lighter transaction")
	}
	return signedTransaction{
		TxType: tx.GetTxType(), TxHash: tx.GetTxHash(), TxInfo: json.RawMessage(info),
	}, nil
}

func boolByte(value bool) uint8 {
	if value {
		return 1
	}
	return 0
}
