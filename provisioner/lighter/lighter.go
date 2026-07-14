package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elliottech/lighter-go/client"
	lighterhttp "github.com/elliottech/lighter-go/client/http"
	"github.com/elliottech/lighter-go/types"
	"github.com/ethereum/go-ethereum/common"
)

const maxLighterResponseBytes = 64 << 10

const maxAccountDiscoveryPages = 32

var (
	errAmbiguousSubmission = errors.New("ambiguous Lighter association submission")
	errLighterRejected     = errors.New("Lighter rejected association")
	errLighterHashMismatch = errors.New("Lighter association hash mismatch")
)

type lighterClient interface {
	DiscoverEmptySubaccount(context.Context, string) (int64, error)
	NextNonce(context.Context, int64, uint8) (int64, error)
	GenerateKey() (string, string, error)
	BuildAssociation(string, string, int64, uint8, int64, int64) (association, error)
	FinalizeAssociation(string, string, int64, uint8, int64, int64, string) (association, string, error)
	Broadcast(context.Context, association) error
	RegisteredPublicKey(int64, uint8) (string, error)
	AuthToken(string, int64, uint8, time.Time) (string, error)
	ObserveAccount(context.Context, string, int64, uint8, uint16, uint64) (lighterAccountState, error)
	SignCreateOrder(string, int64, uint8, createOrderRequest) (signedTransaction, error)
	SignCancelOrder(string, int64, uint8, cancelOrderRequest) (signedTransaction, error)
	SignCancelAll(string, int64, uint8, cancelAllRequest) (signedTransaction, error)
}

type lighterAccount struct {
	AccountType             int8   `json:"account_type"`
	Index                   int64  `json:"index"`
	AccountIndex            int64  `json:"account_index"`
	L1Address               string `json:"l1_address"`
	TotalOrderCount         int64  `json:"total_order_count"`
	TotalIsolatedOrderCount int64  `json:"total_isolated_order_count"`
	PendingOrderCount       int64  `json:"pending_order_count"`
	AvailableBalance        string `json:"available_balance"`
	Status                  uint8  `json:"status"`
	Collateral              string `json:"collateral"`
	Positions               []struct {
		OpenOrderCount         int64  `json:"open_order_count"`
		PendingOrderCount      int64  `json:"pending_order_count"`
		PositionTiedOrderCount int64  `json:"position_tied_order_count"`
		Position               string `json:"position"`
		PositionValue          string `json:"position_value"`
		AllocatedMargin        string `json:"allocated_margin"`
	} `json:"positions"`
	Assets []struct {
		Balance       string `json:"balance"`
		LockedBalance string `json:"locked_balance"`
	} `json:"assets"`
	TotalAssetValue string `json:"total_asset_value"`
	CrossAssetValue string `json:"cross_asset_value"`
}

type lighterAccountsByOwner struct {
	Code        int32            `json:"code"`
	Message     string           `json:"message"`
	L1Address   string           `json:"l1_address"`
	SubAccounts []lighterAccount `json:"sub_accounts"`
	NextCursor  string           `json:"next_cursor"`
}

type lighterDetailedAccounts struct {
	Code       int32            `json:"code"`
	Message    string           `json:"message"`
	Accounts   []lighterAccount `json:"accounts"`
	NextCursor string           `json:"next_cursor"`
}

type lighterNextNonce struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
	Nonce   int64  `json:"nonce"`
}

type liveLighterClient struct {
	baseURL  string
	chainID  uint32
	http     *http.Client
	readOnly client.MinimalHTTPClient
}

func newLiveLighterClient(baseURL string, chainID uint32) *liveLighterClient {
	return &liveLighterClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		chainID:  chainID,
		http:     &http.Client{Timeout: 15 * time.Second},
		readOnly: lighterhttp.NewClient(baseURL),
	}
}

func (value *liveLighterClient) DiscoverEmptySubaccount(ctx context.Context, owner string) (int64, error) {
	accounts, err := value.accountsByOwner(ctx, owner)
	if err != nil {
		return 0, err
	}
	if len(accounts) < 2 {
		return 0, errNoEmptySubaccount
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Index < accounts[j].Index })
	masterIndex := accounts[0].Index
	eligible := make([]int64, 0, 1)
	seen := make(map[int64]struct{}, len(accounts))
	for _, account := range accounts {
		if account.Index <= 0 {
			return 0, errors.New("Lighter returned an invalid account index")
		}
		if _, exists := seen[account.Index]; exists {
			return 0, errors.New("Lighter returned a duplicate account index")
		}
		seen[account.Index] = struct{}{}
		if !strings.EqualFold(account.L1Address, owner) {
			return 0, errors.New("Lighter returned an account owned by a different wallet")
		}
		if account.Index == masterIndex || account.AccountType != 1 || !summaryIsEmpty(account) {
			continue
		}
		empty, err := value.detailedAccountIsEmpty(ctx, owner, account.Index)
		if err != nil {
			return 0, err
		}
		if empty {
			eligible = append(eligible, account.Index)
		}
	}
	if len(eligible) == 0 {
		return 0, errNoEmptySubaccount
	}
	if len(eligible) != 1 {
		return 0, errAmbiguousEmptySubaccounts
	}
	return eligible[0], nil
}

func (value *liveLighterClient) NextNonce(ctx context.Context, accountIndex int64, apiKeyIndex uint8) (int64, error) {
	if err := validateAccount(accountIndex, apiKeyIndex); err != nil {
		return 0, err
	}
	var result lighterNextNonce
	if err := value.get(ctx, "/api/v1/nextNonce", url.Values{
		"account_index": {strconv.FormatInt(accountIndex, 10)},
		"api_key_index": {strconv.FormatUint(uint64(apiKeyIndex), 10)},
	}, &result); err != nil {
		return 0, err
	}
	if result.Code != 200 || result.Nonce < 0 {
		return 0, errors.New("Lighter returned an invalid next nonce")
	}
	return result.Nonce, nil
}

func (value *liveLighterClient) accountsByOwner(ctx context.Context, owner string) ([]lighterAccount, error) {
	var accounts []lighterAccount
	cursor := ""
	seenCursors := make(map[string]struct{})
	for page := 0; page < maxAccountDiscoveryPages; page++ {
		params := url.Values{"l1_address": {owner}}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		var result lighterAccountsByOwner
		if err := value.get(ctx, "/api/v1/accountsByL1Address", params, &result); err != nil {
			return nil, err
		}
		if result.Code != 200 || !strings.EqualFold(result.L1Address, owner) {
			return nil, errors.New("Lighter account discovery response did not match the owner")
		}
		accounts = append(accounts, result.SubAccounts...)
		cursor = result.NextCursor
		if cursor == "" {
			return accounts, nil
		}
		if _, exists := seenCursors[cursor]; exists {
			return nil, errors.New("Lighter account discovery cursor repeated")
		}
		seenCursors[cursor] = struct{}{}
	}
	return nil, errors.New("Lighter account discovery exceeded the page limit")
}

func (value *liveLighterClient) detailedAccountIsEmpty(ctx context.Context, owner string, accountIndex int64) (bool, error) {
	var result lighterDetailedAccounts
	if err := value.get(ctx, "/api/v1/account", url.Values{
		"by":          {"index"},
		"value":       {strconv.FormatInt(accountIndex, 10)},
		"active_only": {"true"},
	}, &result); err != nil {
		return false, err
	}
	if result.Code != 200 || len(result.Accounts) != 1 {
		return false, errors.New("Lighter returned an ambiguous account detail response")
	}
	account := result.Accounts[0]
	if account.Index != accountIndex || (account.AccountIndex != 0 && account.AccountIndex != accountIndex) ||
		!strings.EqualFold(account.L1Address, owner) || account.AccountType != 1 || account.Status != 1 {
		return false, errors.New("Lighter account detail response did not match the discovered subaccount")
	}
	if !summaryIsEmpty(account) || !decimalIsZero(account.TotalAssetValue) || !decimalIsZero(account.CrossAssetValue) {
		return false, nil
	}
	for _, position := range account.Positions {
		if position.OpenOrderCount != 0 || position.PendingOrderCount != 0 || position.PositionTiedOrderCount != 0 ||
			!decimalIsZero(position.Position) || !decimalIsZero(position.PositionValue) || !decimalIsZero(position.AllocatedMargin) {
			return false, nil
		}
	}
	for _, asset := range account.Assets {
		if !decimalIsZero(asset.Balance) || !decimalIsZero(asset.LockedBalance) {
			return false, nil
		}
	}
	return true, nil
}

func summaryIsEmpty(account lighterAccount) bool {
	return account.Status == 1 && account.TotalOrderCount == 0 && account.TotalIsolatedOrderCount == 0 &&
		account.PendingOrderCount == 0 && decimalIsZero(account.AvailableBalance) && decimalIsZero(account.Collateral)
}

func decimalIsZero(value string) bool {
	if strings.TrimSpace(value) != value || value == "" {
		return false
	}
	parsed, ok := new(big.Rat).SetString(value)
	return ok && parsed.Sign() == 0
}

func (value *liveLighterClient) get(ctx context.Context, path string, params url.Values, target any) error {
	endpoint, err := url.Parse(value.baseURL + path)
	if err != nil {
		return errors.New("construct Lighter discovery request")
	}
	endpoint.RawQuery = params.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return errors.New("construct Lighter discovery request")
	}
	response, err := value.http.Do(request)
	if err != nil {
		return errors.New("query Lighter account state")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxLighterResponseBytes+1))
	if err != nil || len(body) > maxLighterResponseBytes {
		return errors.New("read Lighter account state")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Lighter account state request failed with status %d", response.StatusCode)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return errors.New("decode Lighter account state")
	}
	return nil
}

func (*liveLighterClient) GenerateKey() (string, string, error) {
	return client.GenerateAPIKey()
}

func (value *liveLighterClient) BuildAssociation(secret, public string, accountIndex int64, apiKeyIndex uint8, nonce, expiresAtMS int64) (association, error) {
	return value.association(secret, public, accountIndex, apiKeyIndex, nonce, expiresAtMS, "")
}

func (value *liveLighterClient) FinalizeAssociation(secret, public string, accountIndex int64, apiKeyIndex uint8, nonce, expiresAtMS int64, signature string) (association, string, error) {
	result, err := value.association(secret, public, accountIndex, apiKeyIndex, nonce, expiresAtMS, signature)
	if err != nil {
		return association{}, "", err
	}
	var info struct {
		L1Sig string `json:"L1Sig"`
	}
	if err := json.Unmarshal(result.TxInfo, &info); err != nil || info.L1Sig == "" {
		return association{}, "", errors.New("association signature missing")
	}

	txClient, err := client.NewTxClient(nil, secret, accountIndex, apiKeyIndex, value.chainID)
	if err != nil {
		return association{}, "", errors.New("initialize Lighter association client")
	}
	pubKey, err := decodePublicKey(public)
	if err != nil {
		return association{}, "", err
	}
	options := &types.TransactOpts{Nonce: &nonce, ExpiredAt: expiresAtMS}
	tx, err := txClient.GetChangePubKeyTransaction(&types.ChangePubKeyReq{PubKey: pubKey}, options)
	if err != nil {
		return association{}, "", errors.New("construct Lighter association")
	}
	tx.L1Sig = signature
	owner := tx.GetL1AddressBySignature()
	if owner == (common.Address{}) {
		return association{}, "", errors.New("invalid L1 association signature")
	}
	return result, strings.ToLower(owner.Hex()), nil
}

func (value *liveLighterClient) association(secret, public string, accountIndex int64, apiKeyIndex uint8, nonce, expiresAtMS int64, signature string) (association, error) {
	txClient, err := client.NewTxClient(nil, secret, accountIndex, apiKeyIndex, value.chainID)
	if err != nil {
		return association{}, errors.New("initialize Lighter association client")
	}
	pubKey, err := decodePublicKey(public)
	if err != nil {
		return association{}, err
	}
	options := &types.TransactOpts{Nonce: &nonce, ExpiredAt: expiresAtMS}
	tx, err := txClient.GetChangePubKeyTransaction(&types.ChangePubKeyReq{PubKey: pubKey}, options)
	if err != nil {
		return association{}, errors.New("construct Lighter association")
	}
	tx.L1Sig = signature
	txInfo, err := tx.GetTxInfo()
	if err != nil {
		return association{}, errors.New("encode Lighter association")
	}
	return association{
		TxType:        tx.GetTxType(),
		TxHash:        tx.GetTxHash(),
		TxInfo:        []byte(txInfo),
		MessageToSign: tx.GetL1SignatureBody(),
	}, nil
}

func decodePublicKey(value string) ([40]byte, error) {
	var result [40]byte
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil || len(decoded) != len(result) {
		return result, errors.New("invalid generated Lighter public key")
	}
	copy(result[:], decoded)
	return result, nil
}

func (value *liveLighterClient) Broadcast(ctx context.Context, tx association) error {
	form := url.Values{
		"tx_type":          {strconv.Itoa(int(tx.TxType))},
		"tx_info":          {string(tx.TxInfo)},
		"price_protection": {"true"},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, value.baseURL+"/api/v1/sendTx", strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("construct Lighter association request")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := value.http.Do(request)
	if err != nil {
		return errAmbiguousSubmission
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxLighterResponseBytes+1))
	if err != nil || len(body) > maxLighterResponseBytes {
		return errAmbiguousSubmission
	}
	var result struct {
		Code    int32  `json:"code"`
		Message string `json:"message"`
		TxHash  string `json:"tx_hash"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return errAmbiguousSubmission
	}
	if response.StatusCode >= 500 || response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests {
		return errAmbiguousSubmission
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || result.Code != 200 {
		return fmt.Errorf("%w: code %d", errLighterRejected, result.Code)
	}
	if result.TxHash == "" || !strings.EqualFold(result.TxHash, tx.TxHash) {
		return errLighterHashMismatch
	}
	return nil
}

func (value *liveLighterClient) RegisteredPublicKey(accountIndex int64, apiKeyIndex uint8) (string, error) {
	return value.readOnly.GetApiKey(accountIndex, apiKeyIndex)
}

func (value *liveLighterClient) AuthToken(secret string, accountIndex int64, apiKeyIndex uint8, expiresAt time.Time) (string, error) {
	txClient, err := client.NewTxClient(nil, secret, accountIndex, apiKeyIndex, value.chainID)
	if err != nil {
		return "", errors.New("initialize Lighter auth client")
	}
	return txClient.GetAuthToken(expiresAt)
}
