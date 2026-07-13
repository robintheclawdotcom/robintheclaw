package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type config struct {
	Enabled        bool
	ListenAddress  string
	DatabaseURL    string
	KMSKeyID       string
	APIURL         string
	ChainID        uint32
	CallerID       string
	HMACKey        []byte
	AssociationTTL time.Duration
}

func loadConfig() (config, error) {
	value := config{
		Enabled:        strings.EqualFold(os.Getenv("LIGHTER_PROVISIONER_ENABLED"), "true"),
		ListenAddress:  envOr("LISTEN_ADDRESS", "0.0.0.0:8080"),
		AssociationTTL: 10 * time.Minute,
	}
	if !value.Enabled {
		return value, nil
	}

	var err error
	value.HMACKey, err = hex.DecodeString(os.Getenv("LIGHTER_PROVISIONER_HMAC_KEY"))
	if err != nil || len(value.HMACKey) != 32 {
		return config{}, errors.New("LIGHTER_PROVISIONER_HMAC_KEY must be a 32-byte hex key")
	}
	value.CallerID = os.Getenv("PROVISIONER_CALLER_ID")
	if !validCallerID(value.CallerID) {
		return config{}, errors.New("PROVISIONER_CALLER_ID must be a lowercase service identifier")
	}
	value.DatabaseURL = os.Getenv("LIGHTER_PROVISIONER_DATABASE_URL")
	if value.DatabaseURL == "" {
		return config{}, errors.New("LIGHTER_PROVISIONER_DATABASE_URL is required")
	}
	value.KMSKeyID = os.Getenv("AWS_KMS_KEY_ID")
	if value.KMSKeyID == "" {
		return config{}, errors.New("AWS_KMS_KEY_ID is required")
	}
	value.APIURL = envOr("LIGHTER_API_URL", "https://mainnet.zklighter.elliot.ai")
	if err := validateAPIURL(value.APIURL); err != nil {
		return config{}, err
	}
	chainID, err := strconv.ParseUint(os.Getenv("LIGHTER_CHAIN_ID"), 10, 32)
	if err != nil || (chainID != 300 && chainID != 304) {
		return config{}, errors.New("LIGHTER_CHAIN_ID must be 300 or 304")
	}
	value.ChainID = uint32(chainID)
	if raw := os.Getenv("LIGHTER_ASSOCIATION_TTL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed < time.Minute || parsed > 30*time.Minute {
			return config{}, errors.New("LIGHTER_ASSOCIATION_TTL must be between 1m and 30m")
		}
		value.AssociationTTL = parsed
	}
	return value, nil
}

func validateAPIURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("LIGHTER_API_URL must be an HTTPS origin")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return fmt.Errorf("LIGHTER_API_URL must not contain a path")
	}
	return nil
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
