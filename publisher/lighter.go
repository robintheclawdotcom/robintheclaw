package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const lighterMainnetURL = "https://mainnet.zklighter.elliot.ai"

type LighterClient struct {
	baseURL string
	client  *http.Client
}

type lighterAccounts struct {
	Code     int              `json:"code"`
	Accounts []lighterAccount `json:"accounts"`
}

type lighterAccount struct {
	Index                       uint64            `json:"index"`
	AccountIndex                uint64            `json:"account_index"`
	Status                      uint8             `json:"status"`
	Collateral                  string            `json:"collateral"`
	CrossMaintenanceRequirement string            `json:"cross_maintenance_margin_requirement"`
	TransactionTime             int64             `json:"transaction_time"`
	Positions                   []lighterPosition `json:"positions"`
}

type lighterPosition struct {
	MarketID uint16 `json:"market_id"`
	Position string `json:"position"`
}

type lighterOrders struct {
	Code   int            `json:"code"`
	Orders []lighterOrder `json:"orders"`
}

type lighterOrder struct {
	MarketIndex       uint16 `json:"market_index"`
	OwnerAccountIndex uint64 `json:"owner_account_index"`
	Status            string `json:"status"`
}

type lighterTrades struct {
	Code   int            `json:"code"`
	Trades []lighterTrade `json:"trades"`
}

type lighterTrade struct {
	TradeID      uint64 `json:"trade_id"`
	AskAccountID uint64 `json:"ask_account_id"`
	BidAccountID uint64 `json:"bid_account_id"`
	MarketID     uint16 `json:"market_id"`
}

type lighterNonce struct {
	Code  int    `json:"code"`
	Nonce uint64 `json:"nonce"`
}

func NewLighterClient(baseURL string, client *http.Client) (*LighterClient, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL != lighterMainnetURL && !strings.HasPrefix(baseURL, "http://127.0.0.1:") {
		return nil, errors.New("Lighter URL must be the pinned mainnet API")
	}
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	}
	return &LighterClient{baseURL: baseURL, client: client}, nil
}

func (c *LighterClient) Collect(ctx context.Context, binding LighterBinding) (LighterObservation, error) {
	if binding.AccountIndex == 0 || binding.APIKeyIndex < 2 || binding.APIKeyIndex > 254 {
		return LighterObservation{}, errors.New("invalid Lighter binding")
	}
	tokenBytes, err := readSecretFile(binding.ReadOnlyTokenFile)
	if err != nil {
		return LighterObservation{}, fmt.Errorf("read Lighter read-only token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if !validLighterReadOnlyToken(token, binding.AccountIndex, time.Now()) {
		return LighterObservation{}, errors.New("invalid Lighter read-only token")
	}
	expectedBytes, err := readSecretFile(binding.ExpectedNonceFile)
	if err != nil {
		return LighterObservation{}, fmt.Errorf("read expected nonce: %w", err)
	}
	expectedNonce, err := strconv.ParseUint(strings.TrimSpace(string(expectedBytes)), 10, 64)
	if err != nil {
		return LighterObservation{}, errors.New("invalid expected nonce")
	}

	var accounts lighterAccounts
	if err := c.get(ctx, "/api/v1/account", url.Values{
		"by": {"index"}, "value": {strconv.FormatUint(binding.AccountIndex, 10)}, "active_only": {"false"},
	}, token, &accounts); err != nil {
		return LighterObservation{}, err
	}
	if accounts.Code != 200 || len(accounts.Accounts) != 1 {
		return LighterObservation{}, errors.New("Lighter account reconstruction was incomplete")
	}
	account := accounts.Accounts[0]
	if account.Index != binding.AccountIndex || account.AccountIndex != binding.AccountIndex || account.Status != 1 {
		return LighterObservation{}, errors.New("Lighter returned a mismatched account")
	}

	var orders lighterOrders
	if err := c.get(ctx, "/api/v1/accountActiveOrders", url.Values{
		"account_index": {strconv.FormatUint(binding.AccountIndex, 10)}, "market_id": {"255"}, "market_type": {"all"},
	}, token, &orders); err != nil {
		return LighterObservation{}, err
	}
	if orders.Code != 200 {
		return LighterObservation{}, errors.New("Lighter order reconstruction failed")
	}
	for _, order := range orders.Orders {
		if order.OwnerAccountIndex != binding.AccountIndex {
			return LighterObservation{}, errors.New("Lighter order account mismatch")
		}
	}

	var trades lighterTrades
	if err := c.get(ctx, "/api/v1/trades", url.Values{
		"account_index": {strconv.FormatUint(binding.AccountIndex, 10)}, "market_id": {"255"}, "market_type": {"all"},
		"sort_by": {"trade_id"}, "sort_dir": {"desc"}, "role": {"all"}, "type": {"all"}, "limit": {"100"},
	}, token, &trades); err != nil {
		return LighterObservation{}, err
	}
	if trades.Code != 200 {
		return LighterObservation{}, errors.New("Lighter trade reconstruction failed")
	}
	for _, trade := range trades.Trades {
		if trade.AskAccountID != binding.AccountIndex && trade.BidAccountID != binding.AccountIndex {
			return LighterObservation{}, errors.New("Lighter trade account mismatch")
		}
	}

	var nonce lighterNonce
	if err := c.get(ctx, "/api/v1/nextNonce", url.Values{
		"account_index": {strconv.FormatUint(binding.AccountIndex, 10)},
		"api_key_index": {strconv.FormatUint(uint64(binding.APIKeyIndex), 10)},
	}, token, &nonce); err != nil {
		return LighterObservation{}, err
	}
	if nonce.Code != 200 {
		return LighterObservation{}, errors.New("Lighter nonce reconstruction failed")
	}

	flat := true
	noUnknownPositions := true
	for _, position := range account.Positions {
		size, err := parseUnsignedDecimal(strings.TrimPrefix(position.Position, "-"))
		if err != nil {
			return LighterObservation{}, errors.New("Lighter returned an invalid position")
		}
		if size.Sign() == 0 {
			continue
		}
		flat = false
		noUnknownPositions = false
	}
	lastTradeID := uint64(0)
	for _, trade := range trades.Trades {
		if trade.MarketID != binding.MarketID {
			noUnknownPositions = false
		}
		if trade.TradeID > lastTradeID {
			lastTradeID = trade.TradeID
		}
	}
	ratio, err := marginRatioMicros(account.Collateral, account.CrossMaintenanceRequirement)
	if err != nil {
		return LighterObservation{}, errors.New("Lighter returned invalid margin values")
	}
	noUnknownOrders := len(orders.Orders) == 0
	for _, order := range orders.Orders {
		if order.MarketIndex != binding.MarketID {
			noUnknownOrders = false
		}
	}
	return LighterObservation{
		AccountIndex: binding.AccountIndex, APIKeyIndex: binding.APIKeyIndex,
		Nonce: nonce.Nonce, ExpectedNonce: expectedNonce,
		CollateralRaw: account.Collateral, MaintenanceRequirementRaw: account.CrossMaintenanceRequirement,
		MaintenanceMarginRatioMicros: ratio, NoUnknownOrders: noUnknownOrders,
		NoUnknownPositions: noUnknownPositions, CollateralReady: decimalAtLeast(account.Collateral, binding.MinimumCollateralRaw),
		Flat: flat, RESTReconstructed: true, TradeCount: len(trades.Trades), LastTradeID: lastTradeID,
		StateDigest: EvidenceDigest(struct {
			Account lighterAccount
			Orders  []lighterOrder
			Trades  []lighterTrade
			Nonce   uint64
		}{account, orders.Orders, trades.Trades, nonce.Nonce}),
		ObservedAt: time.Now().UTC(),
	}, nil
}

func validLighterReadOnlyToken(token string, accountIndex uint64, now time.Time) bool {
	parts := strings.Split(token, ":")
	if len(parts) != 5 || parts[0] != "ro" || parts[1] != strconv.FormatUint(accountIndex, 10) ||
		(parts[2] != "single" && parts[2] != "all") {
		return false
	}
	expiresAt, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || expiresAt <= now.Unix()+60 || expiresAt > now.AddDate(10, 0, 1).Unix() || len(parts[4]) < 16 {
		return false
	}
	for _, char := range parts[4] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func (c *LighterClient) get(ctx context.Context, path string, query url.Values, token string, target interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusMethodNotAllowed {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("Lighter returned status %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid Lighter response")
	}
	return nil
}
