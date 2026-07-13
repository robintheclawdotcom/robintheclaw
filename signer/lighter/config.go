package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
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
	provisionerURL        string
	bridgeHMACKey         []byte
	bridgeCallerID        string
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

	var err error
	value.apiHMACKey, err = decodeKey("LIGHTER_SIGNER_HMAC_KEY")
	if err != nil {
		return config{}, err
	}
	value.callerID = os.Getenv("SIGNER_CALLER_ID")
	if !validCallerID(value.callerID) {
		return config{}, errors.New("SIGNER_CALLER_ID must be a lowercase service identifier")
	}
	value.provisionerURL, err = normalizeProvisionerURL(os.Getenv("LIGHTER_PROVISIONER_URL"))
	if err != nil {
		return config{}, err
	}
	value.bridgeHMACKey, err = decodeKey("LIGHTER_SIGNER_BRIDGE_HMAC_KEY")
	if err != nil {
		return config{}, err
	}
	value.bridgeCallerID = os.Getenv("LIGHTER_SIGNER_BRIDGE_CALLER_ID")
	if !validCallerID(value.bridgeCallerID) || value.bridgeCallerID == value.callerID {
		return config{}, errors.New("LIGHTER_SIGNER_BRIDGE_CALLER_ID must be a distinct lowercase service identifier")
	}
	if bytes.Equal(value.apiHMACKey, value.bridgeHMACKey) {
		return config{}, errors.New("coordinator and provisioner bridge HMAC keys must be distinct")
	}
	if raw := os.Getenv("SIGNER_MAX_REQUESTS_PER_MINUTE"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 16)
		if err != nil || parsed == 0 || parsed > 600 {
			return config{}, errors.New("SIGNER_MAX_REQUESTS_PER_MINUTE must be between 1 and 600")
		}
		value.maxRequestsPerMinute = uint16(parsed)
	}
	if raw := os.Getenv("SIGNER_MAX_CONCURRENT_REQUESTS"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 8)
		if err != nil || parsed == 0 || parsed > 16 {
			return config{}, errors.New("SIGNER_MAX_CONCURRENT_REQUESTS must be between 1 and 16")
		}
		value.maxConcurrentRequests = uint8(parsed)
	}
	return value, nil
}

func decodeKey(name string) ([]byte, error) {
	value, err := hex.DecodeString(os.Getenv(name))
	if err != nil || len(value) != 32 {
		return nil, fmt.Errorf("%s must be a 32-byte hex key", name)
	}
	return value, nil
}

func normalizeProvisionerURL(raw string) (string, error) {
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("LIGHTER_PROVISIONER_URL must be an HTTP(S) origin")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("LIGHTER_PROVISIONER_URL must not contain a path")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
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
