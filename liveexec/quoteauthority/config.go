package quoteauthority

import (
	"crypto/ed25519"
	"crypto/hmac"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
)

type Config struct {
	Enabled            bool
	ListenAddress      string
	Caller             string
	AuthKey            []byte
	QuoteSigningKey    ed25519.PrivateKey
	CoordinatorURL     string
	CoordinatorCaller  string
	CoordinatorKey     []byte
	LighterMarketIndex uint32
}

func LoadConfig() (Config, error) {
	enabled, err := strconv.ParseBool(valueOrDefault("ROBIN_QUOTE_AUTHORITY_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("ROBIN_QUOTE_AUTHORITY_ENABLED must be true or false")
	}
	config := Config{Enabled: enabled, ListenAddress: valueOrDefault("ROBIN_QUOTE_AUTHORITY_LISTEN", ":8080")}
	if !enabled {
		return config, nil
	}
	config.Caller = os.Getenv("ROBIN_QUOTE_AUTHORITY_CALLER")
	config.AuthKey, err = decodeBase64("ROBIN_QUOTE_AUTHORITY_HMAC_KEY")
	if err != nil || len(config.AuthKey) < 32 {
		return Config{}, errors.New("ROBIN_QUOTE_AUTHORITY_HMAC_KEY must be at least 32 bytes of base64")
	}
	key, err := decodeBase64("ROBIN_QUOTE_AUTHORITY_ED25519_PRIVATE_KEY")
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return Config{}, errors.New("ROBIN_QUOTE_AUTHORITY_ED25519_PRIVATE_KEY must be a base64 Ed25519 private key")
	}
	if config.Caller == "" {
		return Config{}, errors.New("ROBIN_QUOTE_AUTHORITY_CALLER is required")
	}
	config.CoordinatorURL = os.Getenv("ROBIN_COORDINATOR_URL")
	config.CoordinatorCaller = os.Getenv("ROBIN_COORDINATOR_MARKET_CALLER")
	coordinatorKey := os.Getenv("ROBIN_COORDINATOR_MARKET_HMAC_KEY")
	config.CoordinatorKey, err = hex.DecodeString(coordinatorKey)
	if err != nil || len(config.CoordinatorKey) != 32 || coordinatorKey != hex.EncodeToString(config.CoordinatorKey) {
		return Config{}, errors.New("ROBIN_COORDINATOR_MARKET_HMAC_KEY must be a 32-byte lowercase hex value")
	}
	if _, err := coordinatorEndpoint(config.CoordinatorURL, "/v1/market-quotes"); err != nil {
		return Config{}, err
	}
	if !validCaller(config.Caller) || !validCaller(config.CoordinatorCaller) || config.Caller == config.CoordinatorCaller {
		return Config{}, errors.New("quote authority and coordinator callers must be distinct")
	}
	if hmac.Equal(config.AuthKey, config.CoordinatorKey) {
		return Config{}, errors.New("quote authority and coordinator HMAC keys must be distinct")
	}
	marketIndex := os.Getenv("ROBIN_LIGHTER_AAPL_MARKET_INDEX")
	parsedMarketIndex, parseErr := strconv.ParseUint(marketIndex, 10, 15)
	if marketIndex == "" || parseErr != nil {
		return Config{}, errors.New("ROBIN_LIGHTER_AAPL_MARKET_INDEX must be an explicitly reviewed index between 0 and 32767")
	}
	config.LighterMarketIndex = uint32(parsedMarketIndex)
	config.QuoteSigningKey = ed25519.PrivateKey(key)
	return config, nil
}

func decodeBase64(name string) ([]byte, error) {
	value := os.Getenv(name)
	if value == "" {
		return nil, errors.New("missing environment value")
	}
	return base64.StdEncoding.DecodeString(value)
}

func valueOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
