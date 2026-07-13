package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type accountBinding struct {
	ExecutionAccountID  string `json:"executionAccountId"`
	OwnerAddress        string `json:"ownerAddress"`
	KMSKeyID            string `json:"kmsKeyId"`
	SignerAddress       string `json:"signerAddress"`
	KeyVersion          int64  `json:"keyVersion"`
	FactoryAddress      string `json:"factoryAddress"`
	FactoryCodeHash     string `json:"factoryCodeHash"`
	RegistryAddress     string `json:"registryAddress"`
	RegistryCodeHash    string `json:"registryCodeHash"`
	PolicyDigest        string `json:"policyDigest"`
	VaultAddress        string `json:"vaultAddress"`
	VaultCodeHash       string `json:"vaultCodeHash"`
	RiskManagerAddress  string `json:"riskManagerAddress"`
	RiskManagerCodeHash string `json:"riskManagerCodeHash"`
	SpotAdapterAddress  string `json:"spotAdapterAddress"`
	SpotAdapterCodeHash string `json:"spotAdapterCodeHash"`
	BindingSHA256       string `json:"bindingSha256"`
}

type accountResolver interface {
	Resolve(context.Context, string) (accountBinding, error)
	Ready(context.Context) bool
}

func (value *httpAccountResolver) Ready(ctx context.Context) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, value.baseURL+"/readyz", nil)
	if err != nil {
		return false
	}
	response, err := value.client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return response.StatusCode == http.StatusOK
}

type httpAccountResolver struct {
	baseURL string
	caller  string
	key     []byte
	client  *http.Client
	now     func() time.Time
	random  io.Reader
}

func newHTTPAccountResolver(baseURL, caller string, key []byte) *httpAccountResolver {
	return &httpAccountResolver{
		baseURL: strings.TrimRight(baseURL, "/"),
		caller:  caller,
		key:     append([]byte(nil), key...),
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("provisioner redirect refused")
			},
		},
		now:    time.Now,
		random: rand.Reader,
	}
}

func (value *httpAccountResolver) Resolve(ctx context.Context, executionID string) (accountBinding, error) {
	var result accountBinding
	body, err := json.Marshal(map[string]string{"executionAccountId": executionID})
	if err != nil {
		return result, errors.New("encode account resolution")
	}
	const path = "/v1/signer/resolve"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, value.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return result, errors.New("construct account resolution")
	}
	nonceBytes := make([]byte, 24)
	if _, err := io.ReadFull(value.random, nonceBytes); err != nil {
		return result, errors.New("generate account resolution nonce")
	}
	nonce := hex.EncodeToString(nonceBytes)
	timestamp := fmt.Sprintf("%d", value.now().Unix())
	digest := sha256.Sum256(body)
	canonical := fmt.Sprintf("POST\n%s\n%s\n%s\n%s\n%x", path, value.caller, timestamp, nonce, digest)
	mac := hmac.New(sha256.New, value.key)
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-RTC-Caller", value.caller)
	request.Header.Set("X-RTC-Timestamp", timestamp)
	request.Header.Set("X-RTC-Nonce", nonce)
	request.Header.Set("X-RTC-Signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := value.client.Do(request)
	if err != nil {
		return result, errors.New("account provisioner unavailable")
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(responseBody) > 64<<10 {
		return result, errors.New("invalid account resolution")
	}
	provided, err := hex.DecodeString(response.Header.Get("X-RTC-Response-Signature"))
	if err != nil || len(provided) != sha256.Size {
		return result, errors.New("unauthenticated account resolution")
	}
	responseDigest := sha256.Sum256(responseBody)
	responseCanonical := fmt.Sprintf("RESPONSE\n%s\n%s\n%s\n%d\n%x", path, value.caller, nonce, response.StatusCode, responseDigest)
	responseMAC := hmac.New(sha256.New, value.key)
	_, _ = responseMAC.Write([]byte(responseCanonical))
	if subtle.ConstantTimeCompare(responseMAC.Sum(nil), provided) != 1 {
		return result, errors.New("unauthenticated account resolution")
	}
	if response.StatusCode != http.StatusOK {
		return result, errors.New("execution account is not active")
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return accountBinding{}, errors.New("invalid account resolution")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return accountBinding{}, errors.New("invalid account resolution")
	}
	if err := result.validate(executionID); err != nil {
		return accountBinding{}, err
	}
	return result, nil
}

func (value accountBinding) validate(expectedID string) error {
	if value.ExecutionAccountID != expectedID || !validExecutionAccountID(value.ExecutionAccountID) ||
		value.KMSKeyID == "" || value.KeyVersion <= 0 || !validAddressText(value.OwnerAddress) ||
		!validAddressText(value.SignerAddress) || !validAddressText(value.FactoryAddress) ||
		!validAddressText(value.RegistryAddress) || !validAddressText(value.VaultAddress) ||
		!validAddressText(value.RiskManagerAddress) || !validAddressText(value.SpotAdapterAddress) ||
		!validHashText(value.FactoryCodeHash) || !validHashText(value.RegistryCodeHash) ||
		!validHashText(value.PolicyDigest) || !validHashText(value.VaultCodeHash) ||
		!validHashText(value.RiskManagerCodeHash) || !validHashText(value.SpotAdapterCodeHash) {
		return errors.New("account provisioner returned an invalid binding")
	}
	roles := []string{value.OwnerAddress, value.SignerAddress, value.FactoryAddress, value.RegistryAddress, value.VaultAddress, value.RiskManagerAddress, value.SpotAdapterAddress}
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		role = strings.ToLower(common.HexToAddress(role).Hex())
		if _, exists := seen[role]; exists {
			return errors.New("account provisioner returned overlapping roles")
		}
		seen[role] = struct{}{}
	}
	withoutDigest := value
	withoutDigest.BindingSHA256 = ""
	encoded, err := json.Marshal(withoutDigest)
	if err != nil {
		return errors.New("encode resolved binding")
	}
	digest := sha256.Sum256(encoded)
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(digest[:])), []byte(value.BindingSHA256)) != 1 {
		return errors.New("account binding digest mismatch")
	}
	return nil
}

func (value accountBinding) accountConfig(base Config) (Config, error) {
	base.ExecutionAccountID = value.ExecutionAccountID
	base.KMSKeyID = value.KMSKeyID
	base.OwnerAddress = common.HexToAddress(value.OwnerAddress)
	base.SignerAddress = common.HexToAddress(value.SignerAddress)
	base.FactoryAddress = common.HexToAddress(value.FactoryAddress)
	base.FactoryCodeHash = common.HexToHash(value.FactoryCodeHash)
	base.RegistryAddress = common.HexToAddress(value.RegistryAddress)
	base.RegistryCodeHash = common.HexToHash(value.RegistryCodeHash)
	base.PolicyDigest = common.HexToHash(value.PolicyDigest)
	base.VaultAddress = common.HexToAddress(value.VaultAddress)
	base.VaultCodeHash = common.HexToHash(value.VaultCodeHash)
	base.RiskManagerAddress = common.HexToAddress(value.RiskManagerAddress)
	base.RiskManagerCodeHash = common.HexToHash(value.RiskManagerCodeHash)
	base.SpotAdapterAddress = common.HexToAddress(value.SpotAdapterAddress)
	base.SpotAdapterCodeHash = common.HexToHash(value.SpotAdapterCodeHash)
	if err := validateAccountConfig(base); err != nil {
		return Config{}, err
	}
	return base, nil
}
