package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type Config struct {
	Enabled               bool
	ExecutionAccountID    string
	ListenAddress         string
	APIHMACKey            []byte
	CallerID              string
	MaxRequestsPerMinute  uint16
	MaxConcurrentRequests uint8
	DatabaseURL           string
	RPCURL                string
	ReconciliationRPCURL  string
	ChainID               *big.Int
	KMSKeyID              string
	SignerAddress         common.Address
	OwnerAddress          common.Address
	FactoryAddress        common.Address
	FactoryCodeHash       common.Hash
	RegistryAddress       common.Address
	RegistryCodeHash      common.Hash
	PolicyDigest          common.Hash
	VaultAddress          common.Address
	VaultCodeHash         common.Hash
	RiskManagerAddress    common.Address
	RiskManagerCodeHash   common.Hash
	SpotAdapterAddress    common.Address
	SpotAdapterCodeHash   common.Hash
	MaxGasLimit           uint64
	MaxPriorityFee        *big.Int
	MaxFeePerGas          *big.Int
	MaxTransactionCost    *big.Int
	MinimumGasReserve     *big.Int
	MaxReplacementCount   uint16
	MaxReplacementAge     time.Duration
	RequestTimeout        time.Duration
	ReconcileInterval     time.Duration
	ProvisionerURL        string
	BridgeHMACKey         []byte
	BridgeCallerID        string
}

func loadConfig() (Config, error) {
	config := Config{
		Enabled:               strings.EqualFold(os.Getenv("ROBINHOOD_SIGNER_ENABLED"), "true"),
		ListenAddress:         envOr("LISTEN_ADDRESS", "127.0.0.1:8080"),
		MaxGasLimit:           2_000_000,
		MaxReplacementCount:   3,
		MaxReplacementAge:     10 * time.Minute,
		MaxRequestsPerMinute:  60,
		MaxConcurrentRequests: 4,
		RequestTimeout:        15 * time.Second,
		ReconcileInterval:     5 * time.Second,
	}
	if !config.Enabled {
		return config, nil
	}
	var err error
	encodedHMACKey := os.Getenv("ROBINHOOD_SIGNER_HMAC_KEY")
	config.APIHMACKey, err = hex.DecodeString(encodedHMACKey)
	if err != nil || len(config.APIHMACKey) != 32 {
		return Config{}, errors.New("ROBINHOOD_SIGNER_HMAC_KEY must be a 32-byte hex key")
	}
	config.CallerID = os.Getenv("SIGNER_CALLER_ID")
	if !validCallerID(config.CallerID) {
		return Config{}, errors.New("SIGNER_CALLER_ID must be a lowercase service identifier")
	}
	config.DatabaseURL = os.Getenv("DATABASE_URL")
	config.RPCURL = os.Getenv("ROBINHOOD_RPC_URL")
	config.ReconciliationRPCURL = os.Getenv("ROBINHOOD_RECONCILIATION_RPC_URL")
	chainID, ok := new(big.Int).SetString(os.Getenv("ROBINHOOD_CHAIN_ID"), 10)
	if !ok || chainID.Sign() <= 0 {
		return Config{}, errors.New("ROBINHOOD_CHAIN_ID must be positive")
	}
	config.ChainID = chainID

	if config.DatabaseURL == "" {
		return Config{}, errors.New("signer database is required")
	}
	config.ProvisionerURL, err = normalizeProvisionerURL(os.Getenv("ROBINHOOD_PROVISIONER_URL"))
	if err != nil {
		return Config{}, err
	}
	config.BridgeHMACKey, err = hex.DecodeString(os.Getenv("ROBINHOOD_SIGNER_BRIDGE_HMAC_KEY"))
	if err != nil || len(config.BridgeHMACKey) != 32 {
		return Config{}, errors.New("ROBINHOOD_SIGNER_BRIDGE_HMAC_KEY must be a 32-byte hex key")
	}
	config.BridgeCallerID = os.Getenv("ROBINHOOD_SIGNER_BRIDGE_CALLER_ID")
	if !validCallerID(config.BridgeCallerID) || config.BridgeCallerID == config.CallerID {
		return Config{}, errors.New("ROBINHOOD_SIGNER_BRIDGE_CALLER_ID must be a distinct lowercase service identifier")
	}
	if bytes.Equal(config.APIHMACKey, config.BridgeHMACKey) {
		return Config{}, errors.New("coordinator and provisioner bridge HMAC keys must be distinct")
	}
	if err := validatePrivateRPC(config.RPCURL); err != nil {
		return Config{}, err
	}
	if err := validateReconciliationRPC(config.ReconciliationRPCURL, config.RPCURL); err != nil {
		return Config{}, err
	}
	if value := os.Getenv("ROBINHOOD_MAX_GAS_LIMIT"); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil || parsed == 0 {
			return Config{}, errors.New("ROBINHOOD_MAX_GAS_LIMIT must be positive")
		}
		config.MaxGasLimit = parsed
	}
	if config.MaxPriorityFee, err = requiredPositiveInteger("ROBINHOOD_MAX_PRIORITY_FEE_WEI"); err != nil {
		return Config{}, err
	}
	if config.MaxFeePerGas, err = requiredPositiveInteger("ROBINHOOD_MAX_FEE_PER_GAS_WEI"); err != nil {
		return Config{}, err
	}
	if config.MaxTransactionCost, err = requiredPositiveInteger("ROBINHOOD_MAX_TRANSACTION_COST_WEI"); err != nil {
		return Config{}, err
	}
	if config.MinimumGasReserve, err = requiredPositiveInteger("ROBINHOOD_MINIMUM_GAS_RESERVE_WEI"); err != nil {
		return Config{}, err
	}
	if config.MaxPriorityFee.Cmp(config.MaxFeePerGas) > 0 {
		return Config{}, errors.New("priority fee cap cannot exceed total fee cap")
	}
	if value := os.Getenv("ROBINHOOD_MAX_REPLACEMENTS"); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 16)
		if err != nil || parsed == 0 || parsed > 10 {
			return Config{}, errors.New("ROBINHOOD_MAX_REPLACEMENTS must be between 1 and 10")
		}
		config.MaxReplacementCount = uint16(parsed)
	}
	if value := os.Getenv("ROBINHOOD_MAX_REPLACEMENT_AGE"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed < time.Minute || parsed > time.Hour {
			return Config{}, errors.New("ROBINHOOD_MAX_REPLACEMENT_AGE must be between 1m and 1h")
		}
		config.MaxReplacementAge = parsed
	}
	if value := os.Getenv("SIGNER_MAX_REQUESTS_PER_MINUTE"); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 16)
		if err != nil || parsed == 0 || parsed > 600 {
			return Config{}, errors.New("SIGNER_MAX_REQUESTS_PER_MINUTE must be between 1 and 600")
		}
		config.MaxRequestsPerMinute = uint16(parsed)
	}
	if value := os.Getenv("SIGNER_MAX_CONCURRENT_REQUESTS"); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 8)
		if err != nil || parsed == 0 || parsed > 16 {
			return Config{}, errors.New("SIGNER_MAX_CONCURRENT_REQUESTS must be between 1 and 16")
		}
		config.MaxConcurrentRequests = uint8(parsed)
	}
	return config, nil
}

func validateAccountConfig(config Config) error {
	if !validExecutionAccountID(config.ExecutionAccountID) || config.KMSKeyID == "" ||
		config.SignerAddress == (common.Address{}) || config.OwnerAddress == (common.Address{}) ||
		config.FactoryAddress == (common.Address{}) || config.RegistryAddress == (common.Address{}) ||
		config.VaultAddress == (common.Address{}) || config.RiskManagerAddress == (common.Address{}) ||
		config.SpotAdapterAddress == (common.Address{}) || config.VaultCodeHash == (common.Hash{}) ||
		config.RiskManagerCodeHash == (common.Hash{}) || config.SpotAdapterCodeHash == (common.Hash{}) ||
		config.FactoryCodeHash == (common.Hash{}) || config.RegistryCodeHash == (common.Hash{}) ||
		config.PolicyDigest == (common.Hash{}) {
		return errors.New("Robinhood execution account binding is incomplete")
	}
	roles := []common.Address{config.SignerAddress, config.OwnerAddress, config.FactoryAddress, config.RegistryAddress}
	for index, role := range roles {
		for _, other := range roles[index+1:] {
			if role == other {
				return errors.New("signer, owner, factory, and registry must be distinct")
			}
		}
	}
	return nil
}

func validAddressText(value string) bool {
	return common.IsHexAddress(value) && common.HexToAddress(value) != (common.Address{})
}

func validHashText(value string) bool {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") {
		return false
	}
	decoded, err := hex.DecodeString(value[2:])
	return err == nil && common.BytesToHash(decoded) != (common.Hash{})
}

type deploymentManifest struct {
	ExecutionAccountID  string `json:"execution_account_id"`
	ChainID             string `json:"chain_id"`
	Signer              string `json:"signer"`
	Owner               string `json:"owner"`
	Factory             string `json:"factory"`
	FactoryCodeHash     string `json:"factory_code_hash"`
	Registry            string `json:"registry"`
	RegistryCodeHash    string `json:"registry_code_hash"`
	PolicyDigest        string `json:"policy_digest"`
	Vault               string `json:"vault"`
	VaultCodeHash       string `json:"vault_code_hash"`
	RiskManager         string `json:"risk_manager"`
	RiskManagerCodeHash string `json:"risk_manager_code_hash"`
	SpotAdapter         string `json:"spot_adapter"`
	SpotAdapterCodeHash string `json:"spot_adapter_code_hash"`
}

func (config Config) manifest() (deploymentManifest, string) {
	manifest := deploymentManifest{
		ExecutionAccountID:  config.ExecutionAccountID,
		ChainID:             config.ChainID.String(),
		Signer:              strings.ToLower(config.SignerAddress.Hex()),
		Owner:               strings.ToLower(config.OwnerAddress.Hex()),
		Factory:             strings.ToLower(config.FactoryAddress.Hex()),
		FactoryCodeHash:     strings.ToLower(config.FactoryCodeHash.Hex()),
		Registry:            strings.ToLower(config.RegistryAddress.Hex()),
		RegistryCodeHash:    strings.ToLower(config.RegistryCodeHash.Hex()),
		PolicyDigest:        strings.ToLower(config.PolicyDigest.Hex()),
		Vault:               strings.ToLower(config.VaultAddress.Hex()),
		VaultCodeHash:       strings.ToLower(config.VaultCodeHash.Hex()),
		RiskManager:         strings.ToLower(config.RiskManagerAddress.Hex()),
		RiskManagerCodeHash: strings.ToLower(config.RiskManagerCodeHash.Hex()),
		SpotAdapter:         strings.ToLower(config.SpotAdapterAddress.Hex()),
		SpotAdapterCodeHash: strings.ToLower(config.SpotAdapterCodeHash.Hex()),
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	return manifest, hex.EncodeToString(digest[:])
}

func validExecutionAccountID(value string) bool {
	if len(value) != 36 {
		return false
	}
	normalized := strings.ToLower(value)
	if normalized[14] != '4' || !strings.ContainsRune("89ab", rune(normalized[19])) {
		return false
	}
	for index, character := range normalized {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if (character < 'a' || character > 'f') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func normalizeProvisionerURL(raw string) (string, error) {
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("ROBINHOOD_PROVISIONER_URL must be an HTTP(S) origin")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("ROBINHOOD_PROVISIONER_URL must not contain a path")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validCallerID(value string) bool {
	if len(value) < 3 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func validatePrivateRPC(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("ROBINHOOD_RPC_URL must be an authenticated HTTPS endpoint")
	}
	if parsed.Host == "rpc.mainnet.chain.robinhood.com" || parsed.Host == "rpc.testnet.chain.robinhood.com" {
		return errors.New("public Robinhood RPC is not permitted")
	}
	return nil
}

func validateReconciliationRPC(value, primary string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("ROBINHOOD_RECONCILIATION_RPC_URL must be an HTTPS endpoint")
	}
	if value == primary {
		return errors.New("primary and reconciliation RPC endpoints must be independent")
	}
	return nil
}

func requiredPositiveInteger(name string) (*big.Int, error) {
	value, ok := new(big.Int).SetString(os.Getenv(name), 10)
	if !ok || value.Sign() <= 0 {
		return nil, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
