package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type config struct {
	enabled               bool
	listenAddress         string
	apiHMACKey            []byte
	callerID              string
	maxRequestsPerMinute  uint16
	maxConcurrentRequests uint8
	privateKey            string
	chainID               uint32
	accountIndex          int64
	apiKeyIndex           uint8
	executionAccountID    string
	accounts              []lighterAccountConfig
}

type lighterAccountConfig struct {
	ExecutionAccountID string `json:"executionAccountId"`
	PrivateKey         string `json:"privateKey"`
	ChainID            uint32 `json:"chainId"`
	AccountIndex       int64  `json:"accountIndex"`
	APIKeyIndex        uint8  `json:"apiKeyIndex"`
}

type lighterAccountRegistry struct {
	Accounts []lighterAccountConfig `json:"accounts"`
}

func loadConfig() (config, error) {
	value := config{
		enabled:               strings.EqualFold(os.Getenv("LIGHTER_SIGNER_ENABLED"), "true"),
		listenAddress:         envOr("LISTEN_ADDRESS", "0.0.0.0:8080"),
		maxRequestsPerMinute:  60,
		maxConcurrentRequests: 4,
	}
	if !value.enabled {
		return value, nil
	}

	encodedHMACKey := os.Getenv("LIGHTER_SIGNER_HMAC_KEY")
	var err error
	value.apiHMACKey, err = hex.DecodeString(encodedHMACKey)
	if err != nil || len(value.apiHMACKey) != 32 {
		return config{}, fmt.Errorf("LIGHTER_SIGNER_HMAC_KEY must be a 32-byte hex key")
	}
	value.callerID = os.Getenv("SIGNER_CALLER_ID")
	if !validCallerID(value.callerID) {
		return config{}, fmt.Errorf("SIGNER_CALLER_ID must be a lowercase service identifier")
	}
	if path := os.Getenv("LIGHTER_SIGNER_ACCOUNTS_FILE"); path != "" {
		value.accounts, err = loadAccountRegistry(path)
		if err != nil {
			return config{}, err
		}
	} else {
		value.privateKey = os.Getenv("LIGHTER_API_PRIVATE_KEY")
		value.executionAccountID = os.Getenv("LIGHTER_EXECUTION_ACCOUNT_ID")
		chainID, chainErr := parseUint("LIGHTER_CHAIN_ID", 32)
		accountIndex, accountErr := parseInt("LIGHTER_ACCOUNT_INDEX", 64)
		apiKeyIndex, keyErr := parseUint("LIGHTER_API_KEY_INDEX", 8)
		legacy := lighterAccountConfig{
			ExecutionAccountID: value.executionAccountID,
			PrivateKey:         value.privateKey,
			ChainID:            uint32(chainID),
			AccountIndex:       accountIndex,
			APIKeyIndex:        uint8(apiKeyIndex),
		}
		if chainErr != nil || accountErr != nil || keyErr != nil || !validLighterAccount(legacy) {
			return config{}, fmt.Errorf("legacy Lighter account binding is invalid")
		}
		value.chainID = legacy.ChainID
		value.accountIndex = legacy.AccountIndex
		value.apiKeyIndex = legacy.APIKeyIndex
		value.accounts = []lighterAccountConfig{legacy}
	}
	if raw := os.Getenv("SIGNER_MAX_REQUESTS_PER_MINUTE"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 16)
		if err != nil || parsed == 0 || parsed > 600 {
			return config{}, fmt.Errorf("SIGNER_MAX_REQUESTS_PER_MINUTE must be between 1 and 600")
		}
		value.maxRequestsPerMinute = uint16(parsed)
	}
	if raw := os.Getenv("SIGNER_MAX_CONCURRENT_REQUESTS"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 8)
		if err != nil || parsed == 0 || parsed > 16 {
			return config{}, fmt.Errorf("SIGNER_MAX_CONCURRENT_REQUESTS must be between 1 and 16")
		}
		value.maxConcurrentRequests = uint8(parsed)
	}
	return value, nil
}

func loadAccountRegistry(path string) ([]lighterAccountConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open Lighter account registry")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("Lighter account registry must be a mode-0600 regular file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var registry lighterAccountRegistry
	if err := decoder.Decode(&registry); err != nil {
		return nil, fmt.Errorf("decode Lighter account registry")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("Lighter account registry must contain one JSON value")
	}
	if len(registry.Accounts) == 0 || len(registry.Accounts) > 1000 {
		return nil, fmt.Errorf("Lighter account registry must contain 1 to 1000 accounts")
	}
	identities := make(map[string]struct{}, len(registry.Accounts))
	venueKeys := make(map[string]struct{}, len(registry.Accounts))
	for _, account := range registry.Accounts {
		if !validLighterAccount(account) {
			return nil, fmt.Errorf("Lighter account registry contains an invalid binding")
		}
		venueKey := fmt.Sprintf("%d:%d:%d", account.ChainID, account.AccountIndex, account.APIKeyIndex)
		if _, exists := identities[account.ExecutionAccountID]; exists {
			return nil, fmt.Errorf("duplicate execution account binding")
		}
		if _, exists := venueKeys[venueKey]; exists {
			return nil, fmt.Errorf("duplicate Lighter venue key binding")
		}
		identities[account.ExecutionAccountID] = struct{}{}
		venueKeys[venueKey] = struct{}{}
	}
	return registry.Accounts, nil
}

func validLighterAccount(account lighterAccountConfig) bool {
	return validExecutionAccountID(account.ExecutionAccountID) && account.PrivateKey != "" &&
		(account.ChainID == 300 || account.ChainID == 304) && account.AccountIndex > 0 &&
		account.APIKeyIndex >= 2 && account.APIKeyIndex <= 254
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

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func parseUint(key string, bits int) (uint64, error) {
	value := os.Getenv(key)
	parsed, err := strconv.ParseUint(value, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("%s is invalid", key)
	}
	return parsed, nil
}

func parseInt(key string, bits int) (int64, error) {
	value := os.Getenv(key)
	parsed, err := strconv.ParseInt(value, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("%s is invalid", key)
	}
	return parsed, nil
}
