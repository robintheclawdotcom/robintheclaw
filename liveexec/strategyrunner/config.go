package strategyrunner

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"strconv"
)

type Config struct {
	Enabled        bool
	ListenAddress  string
	Caller         string
	AuthKey        []byte
	QuotePublicKey ed25519.PublicKey
}

func LoadConfig() (Config, error) {
	enabled, err := strconv.ParseBool(valueOrDefault("ROBIN_STRATEGY_RUNNER_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("ROBIN_STRATEGY_RUNNER_ENABLED must be true or false")
	}
	config := Config{Enabled: enabled, ListenAddress: valueOrDefault("ROBIN_STRATEGY_RUNNER_LISTEN", ":8080")}
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
