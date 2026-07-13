package quoteauthority

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"strconv"
)

type Config struct {
	Enabled         bool
	ListenAddress   string
	Caller          string
	AuthKey         []byte
	QuoteSigningKey ed25519.PrivateKey
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
