package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var errObservationRateLimited = errors.New("Lighter observation rate limited")

type publisherAccountStateRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
}

type publisherAccountStateResponse struct {
	ExecutionAccountID           string    `json:"executionAccountId"`
	AccountIndex                 uint64    `json:"accountIndex"`
	APIKeyIndex                  uint8     `json:"apiKeyIndex"`
	MarketID                     uint16    `json:"marketId"`
	CredentialVersion            int64     `json:"credentialVersion"`
	Nonce                        uint64    `json:"nonce"`
	ExpectedNonce                uint64    `json:"expectedNonce"`
	CollateralRaw                string    `json:"collateralRaw"`
	MaintenanceRequirementRaw    string    `json:"maintenanceRequirementRaw"`
	MaintenanceMarginRatioMicros uint64    `json:"maintenanceMarginRatioMicros"`
	NoUnknownOrders              bool      `json:"noUnknownOrders"`
	NoUnknownPositions           bool      `json:"noUnknownPositions"`
	Flat                         bool      `json:"flat"`
	RESTReconstructed            bool      `json:"restReconstructed"`
	TradeCount                   int       `json:"tradeCount"`
	LastTradeID                  uint64    `json:"lastTradeId"`
	StateDigest                  string    `json:"stateDigest"`
	ObservedAt                   time.Time `json:"observedAt"`
}

type lighterAccountState struct {
	AccountIndex                 uint64
	APIKeyIndex                  uint8
	MarketID                     uint16
	Nonce                        uint64
	ExpectedNonce                uint64
	CollateralRaw                string
	MaintenanceRequirementRaw    string
	MaintenanceMarginRatioMicros uint64
	NoUnknownOrders              bool
	NoUnknownPositions           bool
	Flat                         bool
	RESTReconstructed            bool
	TradeCount                   int
	LastTradeID                  uint64
	StateDigest                  string
	ObservedAt                   time.Time
}

type publisherLighterAccounts struct {
	Code     int                       `json:"code"`
	Accounts []publisherLighterAccount `json:"accounts"`
}

type publisherLighterAccount struct {
	Index                       uint64                     `json:"index"`
	AccountIndex                uint64                     `json:"account_index"`
	Status                      uint8                      `json:"status"`
	Collateral                  string                     `json:"collateral"`
	CrossMaintenanceRequirement string                     `json:"cross_maintenance_margin_requirement"`
	Positions                   []publisherLighterPosition `json:"positions"`
}

type publisherLighterPosition struct {
	MarketID uint16 `json:"market_id"`
	Position string `json:"position"`
}

type publisherLighterOrders struct {
	Code   int                     `json:"code"`
	Orders []publisherLighterOrder `json:"orders"`
}

type publisherLighterOrder struct {
	MarketIndex       uint16 `json:"market_index"`
	OwnerAccountIndex uint64 `json:"owner_account_index"`
}

type publisherLighterTrades struct {
	Code   int                     `json:"code"`
	Trades []publisherLighterTrade `json:"trades"`
}

type publisherLighterTrade struct {
	TradeID      uint64 `json:"trade_id"`
	AskAccountID uint64 `json:"ask_account_id"`
	BidAccountID uint64 `json:"bid_account_id"`
	MarketID     uint16 `json:"market_id"`
}

type publisherLighterNonce struct {
	Code  int    `json:"code"`
	Nonce uint64 `json:"nonce"`
}

func (value *service) publisherAccountState(ctx context.Context, request publisherAccountStateRequest) (publisherAccountStateResponse, error) {
	if err := validateExecutionAccountID(request.ExecutionAccountID); err != nil {
		return publisherAccountStateResponse{}, err
	}
	record, secret, err := value.activeSecret(ctx, request.ExecutionAccountID)
	if err != nil {
		return publisherAccountStateResponse{}, err
	}
	expectedNonce, err := value.store.ExpectedNonce(ctx, record)
	if err != nil {
		zero(secret)
		return publisherAccountStateResponse{}, err
	}
	token, err := value.lighter.AuthToken(transientString(secret), record.AccountIndex, record.APIKeyIndex, value.now().Add(5*time.Minute))
	zero(secret)
	if err != nil {
		return publisherAccountStateResponse{}, errors.New("generate Lighter observation token")
	}
	state, err := value.lighter.ObserveAccount(ctx, token, record.AccountIndex, record.APIKeyIndex, value.publisherMarketID, expectedNonce)
	if err != nil {
		return publisherAccountStateResponse{}, err
	}
	if state.AccountIndex != uint64(record.AccountIndex) || state.APIKeyIndex != record.APIKeyIndex ||
		state.MarketID != value.publisherMarketID || state.ExpectedNonce != expectedNonce {
		_ = value.store.Block(context.WithoutCancel(ctx), record, "publisher_observation_identity_mismatch")
		return publisherAccountStateResponse{}, errors.New("Lighter observation identity mismatch")
	}
	if err := value.store.VerifyActive(ctx, record); err != nil {
		return publisherAccountStateResponse{}, err
	}
	return publisherAccountStateResponse{
		ExecutionAccountID: record.ExecutionAccountID,
		AccountIndex:       state.AccountIndex, APIKeyIndex: state.APIKeyIndex, MarketID: state.MarketID,
		CredentialVersion: record.Version, Nonce: state.Nonce, ExpectedNonce: state.ExpectedNonce,
		CollateralRaw: state.CollateralRaw, MaintenanceRequirementRaw: state.MaintenanceRequirementRaw,
		MaintenanceMarginRatioMicros: state.MaintenanceMarginRatioMicros,
		NoUnknownOrders:              state.NoUnknownOrders, NoUnknownPositions: state.NoUnknownPositions,
		Flat: state.Flat, RESTReconstructed: state.RESTReconstructed,
		TradeCount: state.TradeCount, LastTradeID: state.LastTradeID,
		StateDigest: state.StateDigest, ObservedAt: state.ObservedAt,
	}, nil
}

func (value *liveLighterClient) ObserveAccount(ctx context.Context, token string, accountIndex int64, apiKeyIndex uint8, marketID uint16, expectedNonce uint64) (lighterAccountState, error) {
	if token == "" || accountIndex <= 0 || apiKeyIndex < 4 || apiKeyIndex > 254 || marketID == 0 {
		return lighterAccountState{}, errors.New("invalid Lighter observation binding")
	}
	accountValue := strconv.FormatInt(accountIndex, 10)
	var accounts publisherLighterAccounts
	if err := value.publisherGet(ctx, "/api/v1/account", url.Values{
		"by": {"index"}, "value": {accountValue}, "active_only": {"false"},
	}, token, &accounts); err != nil {
		return lighterAccountState{}, err
	}
	if accounts.Code != 200 || len(accounts.Accounts) != 1 {
		return lighterAccountState{}, errors.New("Lighter account reconstruction was incomplete")
	}
	account := accounts.Accounts[0]
	if account.Index != uint64(accountIndex) || account.AccountIndex != uint64(accountIndex) || account.Status != 1 {
		return lighterAccountState{}, errors.New("Lighter returned a mismatched account")
	}
	var orders publisherLighterOrders
	if err := value.publisherGet(ctx, "/api/v1/accountActiveOrders", url.Values{
		"account_index": {accountValue}, "market_id": {"255"}, "market_type": {"all"},
	}, token, &orders); err != nil {
		return lighterAccountState{}, err
	}
	if orders.Code != 200 {
		return lighterAccountState{}, errors.New("Lighter order reconstruction failed")
	}
	for _, order := range orders.Orders {
		if order.OwnerAccountIndex != uint64(accountIndex) {
			return lighterAccountState{}, errors.New("Lighter order account mismatch")
		}
	}
	var trades publisherLighterTrades
	if err := value.publisherGet(ctx, "/api/v1/trades", url.Values{
		"account_index": {accountValue}, "market_id": {"255"}, "market_type": {"all"},
		"sort_by": {"trade_id"}, "sort_dir": {"desc"}, "role": {"all"}, "type": {"all"}, "limit": {"100"},
	}, token, &trades); err != nil {
		return lighterAccountState{}, err
	}
	if trades.Code != 200 {
		return lighterAccountState{}, errors.New("Lighter trade reconstruction failed")
	}
	for _, trade := range trades.Trades {
		if trade.AskAccountID != uint64(accountIndex) && trade.BidAccountID != uint64(accountIndex) {
			return lighterAccountState{}, errors.New("Lighter trade account mismatch")
		}
	}
	var nonce publisherLighterNonce
	if err := value.publisherGet(ctx, "/api/v1/nextNonce", url.Values{
		"account_index": {accountValue}, "api_key_index": {strconv.FormatUint(uint64(apiKeyIndex), 10)},
	}, token, &nonce); err != nil {
		return lighterAccountState{}, err
	}
	if nonce.Code != 200 {
		return lighterAccountState{}, errors.New("Lighter nonce reconstruction failed")
	}
	flat := true
	noUnknownPositions := true
	for _, position := range account.Positions {
		size, err := publisherDecimal(strings.TrimPrefix(position.Position, "-"))
		if err != nil {
			return lighterAccountState{}, errors.New("Lighter returned an invalid position")
		}
		if size.Sign() == 0 {
			continue
		}
		flat = false
		if position.MarketID != marketID {
			noUnknownPositions = false
		}
	}
	lastTradeID := uint64(0)
	for _, trade := range trades.Trades {
		if trade.MarketID != marketID {
			noUnknownPositions = false
		}
		if trade.TradeID > lastTradeID {
			lastTradeID = trade.TradeID
		}
	}
	ratio, err := publisherMarginRatio(account.Collateral, account.CrossMaintenanceRequirement)
	if err != nil {
		return lighterAccountState{}, errors.New("Lighter returned invalid margin values")
	}
	noUnknownOrders := true
	for _, order := range orders.Orders {
		if order.MarketIndex != marketID {
			noUnknownOrders = false
		}
	}
	digestBody, _ := json.Marshal(struct {
		Account publisherLighterAccount
		Orders  []publisherLighterOrder
		Trades  []publisherLighterTrade
		Nonce   uint64
	}{account, orders.Orders, trades.Trades, nonce.Nonce})
	digest := sha256.Sum256(digestBody)
	return lighterAccountState{
		AccountIndex: uint64(accountIndex), APIKeyIndex: apiKeyIndex, MarketID: marketID,
		Nonce: nonce.Nonce, ExpectedNonce: expectedNonce, CollateralRaw: account.Collateral,
		MaintenanceRequirementRaw: account.CrossMaintenanceRequirement, MaintenanceMarginRatioMicros: ratio,
		NoUnknownOrders: noUnknownOrders, NoUnknownPositions: noUnknownPositions,
		Flat: flat, RESTReconstructed: true, TradeCount: len(trades.Trades), LastTradeID: lastTradeID,
		StateDigest: hex.EncodeToString(digest[:]), ObservedAt: time.Now().UTC(),
	}, nil
}

func (value *liveLighterClient) publisherGet(ctx context.Context, path string, query url.Values, token string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, value.baseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return errors.New("construct Lighter observation request")
	}
	request.Header.Set("Authorization", token)
	request.Header.Set("Accept", "application/json")
	response, err := value.http.Do(request)
	if err != nil {
		return errors.New("Lighter observation unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return errObservationRateLimited
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Lighter observation returned status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxLighterResponseBytes+1))
	if err != nil || len(body) > maxLighterResponseBytes {
		return errors.New("invalid Lighter observation response")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid Lighter observation response")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid Lighter observation response")
	}
	return nil
}

func publisherDecimal(value string) (*big.Rat, error) {
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "eE+") {
		return nil, errors.New("invalid decimal")
	}
	result, ok := new(big.Rat).SetString(value)
	if !ok || result.Sign() < 0 {
		return nil, errors.New("invalid decimal")
	}
	return result, nil
}

func publisherMarginRatio(collateral, maintenance string) (uint64, error) {
	collateralValue, err := publisherDecimal(collateral)
	if err != nil {
		return 0, err
	}
	maintenanceValue, err := publisherDecimal(maintenance)
	if err != nil {
		return 0, err
	}
	if maintenanceValue.Sign() == 0 {
		return 10_000_000, nil
	}
	ratio := new(big.Rat).Mul(new(big.Rat).Quo(collateralValue, maintenanceValue), big.NewRat(1_000_000, 1))
	result := new(big.Int).Quo(ratio.Num(), ratio.Denom())
	if !result.IsUint64() {
		return 0, errors.New("margin ratio out of range")
	}
	return result.Uint64(), nil
}
