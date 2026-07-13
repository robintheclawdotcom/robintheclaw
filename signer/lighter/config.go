package main

import (
	"encoding/hex"
	"fmt"
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
	value.privateKey = os.Getenv("LIGHTER_API_PRIVATE_KEY")
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
