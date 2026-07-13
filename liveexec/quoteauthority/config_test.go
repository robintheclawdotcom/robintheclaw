package quoteauthority

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestConfigDefaultsToDisabled(t *testing.T) {
	t.Setenv("ROBIN_QUOTE_AUTHORITY_ENABLED", "")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled {
		t.Fatal("quote authority enabled by default")
	}
}

func TestEnabledConfigRequiresExplicitReviewedMarketIndex(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
	t.Setenv("ROBIN_QUOTE_AUTHORITY_ENABLED", "true")
	t.Setenv("ROBIN_QUOTE_AUTHORITY_CALLER", "quote-client")
	t.Setenv("ROBIN_QUOTE_AUTHORITY_HMAC_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	t.Setenv("ROBIN_QUOTE_AUTHORITY_ED25519_PRIVATE_KEY", base64.StdEncoding.EncodeToString(privateKey))
	t.Setenv("ROBIN_COORDINATOR_URL", "http://127.0.0.1:8080")
	t.Setenv("ROBIN_COORDINATOR_MARKET_CALLER", "quote-publisher")
	t.Setenv("ROBIN_COORDINATOR_MARKET_HMAC_KEY", hex.EncodeToString(bytes.Repeat([]byte{2}, 32)))
	t.Setenv("ROBIN_LIGHTER_AAPL_MARKET_INDEX", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("missing reviewed market index accepted")
	}
	t.Setenv("ROBIN_LIGHTER_AAPL_MARKET_INDEX", "101")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.LighterMarketIndex != 101 {
		t.Fatal("reviewed market index was not loaded")
	}
}
