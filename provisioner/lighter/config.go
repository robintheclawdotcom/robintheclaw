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
	"time"
)

type config struct {
	Enabled                       bool
	ListenAddress                 string
	DatabaseURL                   string
	RunMigrations                 bool
	KMSKeyID                      string
	APIURL                        string
	ChainID                       uint32
	CallerID                      string
	HMACKey                       []byte
	SignerCallerID                string
	SignerHMACKey                 []byte
	PublisherCallerID             string
	PublisherHMACKey              []byte
	SigningMaxRequestsPerMinute   uint16
	SigningMaxConcurrent          uint8
	PublisherMaxRequestsPerMinute uint16
	PublisherMaxConcurrent        uint8
	PublisherMarketID             uint16
	MarketBaseDecimals            uint8
	MarketPriceDecimals           uint8
	AssociationTTL                time.Duration
}

func loadConfig() (config, error) {
	value := config{
		Enabled:                       strings.EqualFold(os.Getenv("LIGHTER_PROVISIONER_ENABLED"), "true"),
		ListenAddress:                 envOr("LISTEN_ADDRESS", "0.0.0.0:8080"),
		SigningMaxRequestsPerMinute:   120,
		SigningMaxConcurrent:          8,
		PublisherMaxRequestsPerMinute: 600,
		PublisherMaxConcurrent:        16,
		AssociationTTL:                10 * time.Minute,
		RunMigrations:                 true,
	}
	if raw := os.Getenv("LIGHTER_PROVISIONER_RUN_MIGRATIONS"); raw != "" {
		if raw != "true" && raw != "false" {
			return config{}, errors.New("LIGHTER_PROVISIONER_RUN_MIGRATIONS must be true or false")
		}
		value.RunMigrations = raw == "true"
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
	value.SignerHMACKey, err = hex.DecodeString(os.Getenv("LIGHTER_SIGNER_BRIDGE_HMAC_KEY"))
	if err != nil || len(value.SignerHMACKey) != 32 {
		return config{}, errors.New("LIGHTER_SIGNER_BRIDGE_HMAC_KEY must be a 32-byte hex key")
	}
	value.SignerCallerID = os.Getenv("LIGHTER_SIGNER_BRIDGE_CALLER_ID")
	if !validCallerID(value.SignerCallerID) || value.SignerCallerID == value.CallerID {
		return config{}, errors.New("LIGHTER_SIGNER_BRIDGE_CALLER_ID must be a distinct lowercase service identifier")
	}
	if bytes.Equal(value.HMACKey, value.SignerHMACKey) {
		return config{}, errors.New("product and signer bridge HMAC keys must be distinct")
	}
	value.PublisherHMACKey, err = hex.DecodeString(os.Getenv("LIGHTER_PUBLISHER_BRIDGE_HMAC_KEY"))
	if err != nil || len(value.PublisherHMACKey) != 32 {
		return config{}, errors.New("LIGHTER_PUBLISHER_BRIDGE_HMAC_KEY must be a 32-byte hex key")
	}
	value.PublisherCallerID = os.Getenv("LIGHTER_PUBLISHER_BRIDGE_CALLER_ID")
	if !validCallerID(value.PublisherCallerID) || value.PublisherCallerID == value.CallerID || value.PublisherCallerID == value.SignerCallerID {
		return config{}, errors.New("LIGHTER_PUBLISHER_BRIDGE_CALLER_ID must be a distinct lowercase service identifier")
	}
	if bytes.Equal(value.PublisherHMACKey, value.HMACKey) || bytes.Equal(value.PublisherHMACKey, value.SignerHMACKey) {
		return config{}, errors.New("publisher bridge HMAC key must be distinct")
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
	marketID, err := strconv.ParseUint(os.Getenv("LIGHTER_PUBLISHER_MARKET_ID"), 10, 16)
	if err != nil || marketID == 0 || marketID >= 255 {
		return config{}, errors.New("LIGHTER_PUBLISHER_MARKET_ID must be between 1 and 254")
	}
	value.PublisherMarketID = uint16(marketID)
	baseDecimals, err := strconv.ParseUint(os.Getenv("LIGHTER_AAPL_BASE_DECIMALS"), 10, 8)
	if err != nil || baseDecimals > 18 {
		return config{}, errors.New("LIGHTER_AAPL_BASE_DECIMALS must be between 0 and 18")
	}
	value.MarketBaseDecimals = uint8(baseDecimals)
	priceDecimals, err := strconv.ParseUint(os.Getenv("LIGHTER_AAPL_PRICE_DECIMALS"), 10, 8)
	if err != nil || priceDecimals > 18 {
		return config{}, errors.New("LIGHTER_AAPL_PRICE_DECIMALS must be between 0 and 18")
	}
	value.MarketPriceDecimals = uint8(priceDecimals)
	if raw := os.Getenv("LIGHTER_ASSOCIATION_TTL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed < time.Minute || parsed > 30*time.Minute {
			return config{}, errors.New("LIGHTER_ASSOCIATION_TTL must be between 1m and 30m")
		}
		value.AssociationTTL = parsed
	}
	if raw := os.Getenv("LIGHTER_SIGNING_MAX_REQUESTS_PER_MINUTE"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 16)
		if err != nil || parsed == 0 || parsed > 1200 {
			return config{}, errors.New("LIGHTER_SIGNING_MAX_REQUESTS_PER_MINUTE must be between 1 and 1200")
		}
		value.SigningMaxRequestsPerMinute = uint16(parsed)
	}
	if raw := os.Getenv("LIGHTER_SIGNING_MAX_CONCURRENT"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 8)
		if err != nil || parsed == 0 || parsed > 32 {
			return config{}, errors.New("LIGHTER_SIGNING_MAX_CONCURRENT must be between 1 and 32")
		}
		value.SigningMaxConcurrent = uint8(parsed)
	}
	if raw := os.Getenv("LIGHTER_PUBLISHER_MAX_REQUESTS_PER_MINUTE"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 16)
		if err != nil || parsed == 0 || parsed > 6000 {
			return config{}, errors.New("LIGHTER_PUBLISHER_MAX_REQUESTS_PER_MINUTE must be between 1 and 6000")
		}
		value.PublisherMaxRequestsPerMinute = uint16(parsed)
	}
	if raw := os.Getenv("LIGHTER_PUBLISHER_MAX_CONCURRENT"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 8)
		if err != nil || parsed == 0 || parsed > 64 {
			return config{}, errors.New("LIGHTER_PUBLISHER_MAX_CONCURRENT must be between 1 and 64")
		}
		value.PublisherMaxConcurrent = uint8(parsed)
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
