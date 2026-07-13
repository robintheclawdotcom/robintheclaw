package main

import (
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
	TimelockAddress       common.Address
	RecoveryAddress       common.Address
	GuardianAddress       common.Address
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
	config.KMSKeyID = os.Getenv("AWS_KMS_KEY_ID")
	chainID, ok := new(big.Int).SetString(os.Getenv("ROBINHOOD_CHAIN_ID"), 10)
	if !ok || chainID.Sign() <= 0 {
		return Config{}, errors.New("ROBINHOOD_CHAIN_ID must be positive")
	}
	config.ChainID = chainID

	if config.SignerAddress, err = requiredAddress("ROBINHOOD_SIGNER_ADDRESS"); err != nil {
		return Config{}, err
	}
	if config.TimelockAddress, err = requiredAddress("ROBINHOOD_TIMELOCK_ADDRESS"); err != nil {
		return Config{}, err
	}
	if config.RecoveryAddress, err = requiredAddress("ROBINHOOD_RECOVERY_ADDRESS"); err != nil {
		return Config{}, err
	}
	if config.GuardianAddress, err = requiredAddress("ROBINHOOD_GUARDIAN_ADDRESS"); err != nil {
		return Config{}, err
	}
	if config.VaultAddress, err = requiredAddress("ROBINHOOD_VAULT_ADDRESS"); err != nil {
		return Config{}, err
	}
	if config.RiskManagerAddress, err = requiredAddress("ROBINHOOD_RISK_MANAGER_ADDRESS"); err != nil {
		return Config{}, err
	}
	if config.SpotAdapterAddress, err = requiredAddress("ROBINHOOD_SPOT_ADAPTER_ADDRESS"); err != nil {
		return Config{}, err
	}
	if config.VaultCodeHash, err = requiredHash("ROBINHOOD_VAULT_CODE_HASH"); err != nil {
		return Config{}, err
	}
	if config.RiskManagerCodeHash, err = requiredHash("ROBINHOOD_RISK_MANAGER_CODE_HASH"); err != nil {
		return Config{}, err
	}
	if config.SpotAdapterCodeHash, err = requiredHash("ROBINHOOD_SPOT_ADAPTER_CODE_HASH"); err != nil {
		return Config{}, err
	}
	if config.DatabaseURL == "" || config.KMSKeyID == "" {
		return Config{}, errors.New("signer database and KMS key are required")
	}
	roles := []common.Address{
		config.SignerAddress,
		config.TimelockAddress,
		config.RecoveryAddress,
		config.GuardianAddress,
	}
	for i, role := range roles {
		for _, other := range roles[i+1:] {
			if role == other {
				return Config{}, errors.New("signer, timelock, recovery, and guardian roles must be distinct")
			}
		}
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

type deploymentManifest struct {
	ChainID             string `json:"chain_id"`
	Signer              string `json:"signer"`
	Timelock            string `json:"timelock"`
	Recovery            string `json:"recovery"`
	Guardian            string `json:"guardian"`
	Vault               string `json:"vault"`
	VaultCodeHash       string `json:"vault_code_hash"`
	RiskManager         string `json:"risk_manager"`
	RiskManagerCodeHash string `json:"risk_manager_code_hash"`
	SpotAdapter         string `json:"spot_adapter"`
	SpotAdapterCodeHash string `json:"spot_adapter_code_hash"`
}

func (config Config) manifest() (deploymentManifest, string) {
	manifest := deploymentManifest{
		ChainID:             config.ChainID.String(),
		Signer:              strings.ToLower(config.SignerAddress.Hex()),
		Timelock:            strings.ToLower(config.TimelockAddress.Hex()),
		Recovery:            strings.ToLower(config.RecoveryAddress.Hex()),
		Guardian:            strings.ToLower(config.GuardianAddress.Hex()),
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

func requiredAddress(name string) (common.Address, error) {
	value := os.Getenv(name)
	if !common.IsHexAddress(value) || common.HexToAddress(value) == (common.Address{}) {
		return common.Address{}, fmt.Errorf("%s must be a non-zero address", name)
	}
	return common.HexToAddress(value), nil
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

func requiredHash(name string) (common.Hash, error) {
	value := os.Getenv(name)
	if len(value) != 66 || !strings.HasPrefix(value, "0x") {
		return common.Hash{}, fmt.Errorf("%s must be a bytes32 hash", name)
	}
	if _, err := hex.DecodeString(value[2:]); err != nil {
		return common.Hash{}, fmt.Errorf("%s must be a bytes32 hash", name)
	}
	return common.HexToHash(value), nil
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
