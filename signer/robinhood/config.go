package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	Accounts              []robinhoodAccountBinding
}

type robinhoodAccountBinding struct {
	ExecutionAccountID  string `json:"executionAccountId"`
	KMSKeyID            string `json:"kmsKeyId"`
	SignerAddress       string `json:"signerAddress"`
	TimelockAddress     string `json:"timelockAddress"`
	RecoveryAddress     string `json:"recoveryAddress"`
	GuardianAddress     string `json:"guardianAddress"`
	VaultAddress        string `json:"vaultAddress"`
	VaultCodeHash       string `json:"vaultCodeHash"`
	RiskManagerAddress  string `json:"riskManagerAddress"`
	RiskManagerCodeHash string `json:"riskManagerCodeHash"`
	SpotAdapterAddress  string `json:"spotAdapterAddress"`
	SpotAdapterCodeHash string `json:"spotAdapterCodeHash"`
}

type robinhoodAccountRegistry struct {
	Accounts []robinhoodAccountBinding `json:"accounts"`
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
	if path := os.Getenv("ROBINHOOD_SIGNER_ACCOUNTS_FILE"); path != "" {
		config.Accounts, err = loadRobinhoodAccountRegistry(path)
		if err != nil {
			return Config{}, err
		}
	} else {
		config.ExecutionAccountID = os.Getenv("ROBINHOOD_EXECUTION_ACCOUNT_ID")
		config.KMSKeyID = os.Getenv("AWS_KMS_KEY_ID")
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
		if err := validateAccountConfig(config); err != nil {
			return Config{}, err
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
	if _, err := config.accountConfigs(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func loadRobinhoodAccountRegistry(path string) ([]robinhoodAccountBinding, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("open Robinhood account registry")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("Robinhood account registry must be an owner-only regular file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var registry robinhoodAccountRegistry
	if err := decoder.Decode(&registry); err != nil {
		return nil, errors.New("decode Robinhood account registry")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("Robinhood account registry must contain one JSON value")
	}
	if len(registry.Accounts) == 0 || len(registry.Accounts) > 1000 {
		return nil, errors.New("Robinhood account registry must contain 1 to 1000 accounts")
	}
	identities := make(map[string]struct{}, len(registry.Accounts))
	signers := make(map[string]struct{}, len(registry.Accounts))
	vaults := make(map[string]struct{}, len(registry.Accounts))
	for _, account := range registry.Accounts {
		if !validExecutionAccountID(account.ExecutionAccountID) || account.KMSKeyID == "" ||
			!validAddressText(account.SignerAddress) || !validAddressText(account.TimelockAddress) ||
			!validAddressText(account.RecoveryAddress) || !validAddressText(account.GuardianAddress) ||
			!validAddressText(account.VaultAddress) || !validAddressText(account.RiskManagerAddress) ||
			!validAddressText(account.SpotAdapterAddress) || !validHashText(account.VaultCodeHash) ||
			!validHashText(account.RiskManagerCodeHash) || !validHashText(account.SpotAdapterCodeHash) {
			return nil, errors.New("Robinhood account registry contains an invalid binding")
		}
		signer := strings.ToLower(account.SignerAddress)
		vault := strings.ToLower(account.VaultAddress)
		if _, exists := identities[account.ExecutionAccountID]; exists {
			return nil, errors.New("duplicate execution account binding")
		}
		if _, exists := signers[signer]; exists {
			return nil, errors.New("duplicate Robinhood signer binding")
		}
		if _, exists := vaults[vault]; exists {
			return nil, errors.New("duplicate Robinhood vault binding")
		}
		identities[account.ExecutionAccountID] = struct{}{}
		signers[signer] = struct{}{}
		vaults[vault] = struct{}{}
	}
	return registry.Accounts, nil
}

func (config Config) accountConfigs() ([]Config, error) {
	if len(config.Accounts) == 0 {
		if err := validateAccountConfig(config); err != nil {
			return nil, err
		}
		return []Config{config}, nil
	}
	accounts := make([]Config, 0, len(config.Accounts))
	for _, binding := range config.Accounts {
		account := config
		account.Accounts = nil
		account.ExecutionAccountID = binding.ExecutionAccountID
		account.KMSKeyID = binding.KMSKeyID
		account.SignerAddress = common.HexToAddress(binding.SignerAddress)
		account.TimelockAddress = common.HexToAddress(binding.TimelockAddress)
		account.RecoveryAddress = common.HexToAddress(binding.RecoveryAddress)
		account.GuardianAddress = common.HexToAddress(binding.GuardianAddress)
		account.VaultAddress = common.HexToAddress(binding.VaultAddress)
		account.VaultCodeHash = common.HexToHash(binding.VaultCodeHash)
		account.RiskManagerAddress = common.HexToAddress(binding.RiskManagerAddress)
		account.RiskManagerCodeHash = common.HexToHash(binding.RiskManagerCodeHash)
		account.SpotAdapterAddress = common.HexToAddress(binding.SpotAdapterAddress)
		account.SpotAdapterCodeHash = common.HexToHash(binding.SpotAdapterCodeHash)
		if err := validateAccountConfig(account); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

func validateAccountConfig(config Config) error {
	if !validExecutionAccountID(config.ExecutionAccountID) || config.KMSKeyID == "" ||
		config.SignerAddress == (common.Address{}) || config.TimelockAddress == (common.Address{}) ||
		config.RecoveryAddress == (common.Address{}) || config.GuardianAddress == (common.Address{}) ||
		config.VaultAddress == (common.Address{}) || config.RiskManagerAddress == (common.Address{}) ||
		config.SpotAdapterAddress == (common.Address{}) || config.VaultCodeHash == (common.Hash{}) ||
		config.RiskManagerCodeHash == (common.Hash{}) || config.SpotAdapterCodeHash == (common.Hash{}) {
		return errors.New("Robinhood execution account binding is incomplete")
	}
	roles := []common.Address{
		config.SignerAddress,
		config.TimelockAddress,
		config.RecoveryAddress,
		config.GuardianAddress,
	}
	for index, role := range roles {
		for _, other := range roles[index+1:] {
			if role == other {
				return errors.New("signer, timelock, recovery, and guardian roles must be distinct")
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
		ExecutionAccountID:  config.ExecutionAccountID,
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

func validExecutionAccountID(value string) bool {
	if len(value) < 8 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
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
