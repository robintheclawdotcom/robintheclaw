package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type config struct {
	enabled       bool
	listenAddress string
	serviceToken  string
	privateKey    string
	chainID       uint32
	accountIndex  int64
	apiKeyIndex   uint8
}

func loadConfig() (config, error) {
	value := config{
		enabled:       strings.EqualFold(os.Getenv("LIGHTER_SIGNER_ENABLED"), "true"),
		listenAddress: envOr("LISTEN_ADDRESS", "0.0.0.0:8080"),
	}
	if !value.enabled {
		return value, nil
	}

	value.serviceToken = os.Getenv("SIGNER_API_TOKEN")
	value.privateKey = os.Getenv("LIGHTER_API_PRIVATE_KEY")
	if len(value.serviceToken) < 32 {
		return config{}, fmt.Errorf("SIGNER_API_TOKEN must contain at least 32 bytes")
	}
	if value.privateKey == "" {
		return config{}, fmt.Errorf("LIGHTER_API_PRIVATE_KEY is required")
	}

	chainID, err := parseUint("LIGHTER_CHAIN_ID", 32)
	if err != nil {
		return config{}, err
	}
	if chainID != 300 && chainID != 304 {
		return config{}, fmt.Errorf("LIGHTER_CHAIN_ID must be 300 or 304")
	}
	accountIndex, err := parseInt("LIGHTER_ACCOUNT_INDEX", 64)
	if err != nil || accountIndex <= 0 {
		return config{}, fmt.Errorf("LIGHTER_ACCOUNT_INDEX must be positive")
	}
	apiKeyIndex, err := parseUint("LIGHTER_API_KEY_INDEX", 8)
	if err != nil || apiKeyIndex < 2 || apiKeyIndex > 254 {
		return config{}, fmt.Errorf("LIGHTER_API_KEY_INDEX must be between 2 and 254")
	}

	value.chainID = uint32(chainID)
	value.accountIndex = accountIndex
	value.apiKeyIndex = uint8(apiKeyIndex)
	return value, nil
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
