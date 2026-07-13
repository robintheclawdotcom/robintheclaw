package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/elliottech/lighter-go/client"
	lighterhttp "github.com/elliottech/lighter-go/client/http"
	"github.com/elliottech/lighter-go/types"
	"github.com/ethereum/go-ethereum/common"
)

const maxLighterResponseBytes = 64 << 10

var (
	errAmbiguousSubmission = errors.New("ambiguous Lighter association submission")
	errLighterRejected     = errors.New("Lighter rejected association")
	errLighterHashMismatch = errors.New("Lighter association hash mismatch")
)

type lighterClient interface {
	GenerateKey() (string, string, error)
	BuildAssociation(string, string, int64, uint8, int64, int64) (association, error)
	FinalizeAssociation(string, string, int64, uint8, int64, int64, string) (association, string, error)
	Broadcast(context.Context, association) error
	RegisteredPublicKey(int64, uint8) (string, error)
	AuthToken(string, int64, uint8, time.Time) (string, error)
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
