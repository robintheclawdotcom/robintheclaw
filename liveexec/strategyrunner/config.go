package strategyrunner

import (
	"crypto/ed25519"
	"crypto/hmac"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Enabled           bool
	ListenAddress     string
	Caller            string
	AuthKey           []byte
	QuotePublicKey    ed25519.PublicKey
	CoordinatorURL    string
	CoordinatorCaller string
	CoordinatorKey    []byte
}

func LoadConfig() (Config, error) {
	enabled, err := strconv.ParseBool(valueOrDefault("ROBIN_STRATEGY_RUNNER_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("ROBIN_STRATEGY_RUNNER_ENABLED must be true or false")
	}
	config := Config{Enabled: enabled, ListenAddress: valueOrDefault("ROBIN_STRATEGY_RUNNER_LISTEN", ":8080")}
	coordinatorURL := os.Getenv("ROBIN_COORDINATOR_URL")
	coordinatorCaller := os.Getenv("ROBIN_COORDINATOR_INTENT_CALLER")
	coordinatorKey := os.Getenv("ROBIN_COORDINATOR_INTENT_HMAC_KEY")
	configuredCoordinatorValues := 0
	for _, value := range []string{coordinatorURL, coordinatorCaller, coordinatorKey} {
		if value != "" {
			configuredCoordinatorValues++
		}
	}
	if configuredCoordinatorValues != 0 && configuredCoordinatorValues != 3 {
		return Config{}, errors.New("coordinator URL, caller, and HMAC key must be configured together")
	}
	if !enabled {
		return config, nil
	}
	config.Caller = os.Getenv("ROBIN_STRATEGY_RUNNER_CALLER")
	config.AuthKey, err = decodeBase64("ROBIN_STRATEGY_RUNNER_HMAC_KEY")
	if err != nil || len(config.AuthKey) < 32 {
		return Config{}, errors.New("ROBIN_STRATEGY_RUNNER_HMAC_KEY must be at least 32 bytes of base64")
	}
	key, err := decodeBase64("ROBIN_QUOTE_AUTHORITY_ED25519_PUBLIC_KEY")
	if err != nil || len(key) != ed25519.PublicKeySize {
		return Config{}, errors.New("ROBIN_QUOTE_AUTHORITY_ED25519_PUBLIC_KEY must be a base64 Ed25519 public key")
	}
	if config.Caller == "" {
		return Config{}, errors.New("ROBIN_STRATEGY_RUNNER_CALLER is required")
	}
	if configuredCoordinatorValues != 3 {
		return Config{}, errors.New("coordinator persistence configuration is required")
	}
	config.CoordinatorURL = coordinatorURL
	config.CoordinatorCaller = coordinatorCaller
	config.CoordinatorKey, err = hex.DecodeString(coordinatorKey)
	if err != nil || len(config.CoordinatorKey) != 32 || coordinatorKey != strings.ToLower(coordinatorKey) {
		return Config{}, errors.New("ROBIN_COORDINATOR_INTENT_HMAC_KEY must be a 32-byte hex value")
	}
	if _, err := coordinatorEndpoint(config.CoordinatorURL); err != nil {
		return Config{}, err
	}
	if !validCaller(config.Caller) || !validCaller(config.CoordinatorCaller) || config.Caller == config.CoordinatorCaller {
		return Config{}, errors.New("runner and coordinator callers must be distinct lowercase service identifiers")
	}
	if hmac.Equal(config.AuthKey, config.CoordinatorKey) {
		return Config{}, errors.New("runner and coordinator HMAC keys must be distinct")
	}
	config.QuotePublicKey = ed25519.PublicKey(key)
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
