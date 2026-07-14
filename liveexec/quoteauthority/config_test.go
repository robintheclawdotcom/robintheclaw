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
	setEnabledConfig(t)
	t.Setenv("ROBIN_LIGHTER_AAPL_MARKET_INDEX", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("missing reviewed market index accepted")
	}
	t.Setenv("ROBIN_LIGHTER_AAPL_MARKET_INDEX", "101")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.LighterMarketIndex != 101 || config.Adapter.LighterBaseDecimals != 4 || config.Adapter.LighterPriceDecimals != 3 {
		t.Fatal("reviewed market identity was not loaded")
	}
}

func TestEnabledConfigRequiresIndependentRPCOrigins(t *testing.T) {
	setEnabledConfig(t)
	t.Setenv("ROBINHOOD_RECONCILIATION_RPC_URL", "https://rpc-one.invalid/secondary")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("same-origin Robinhood RPCs accepted")
	}
}

func setEnabledConfig(t *testing.T) {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
	t.Setenv("ROBIN_QUOTE_AUTHORITY_ENABLED", "true")
	t.Setenv("ROBIN_QUOTE_AUTHORITY_CALLER", "entry-scheduler")
	t.Setenv("ROBIN_QUOTE_AUTHORITY_HMAC_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	t.Setenv("ROBIN_QUOTE_AUTHORITY_EXIT_CALLER", "exit-publisher")
	t.Setenv("ROBIN_QUOTE_AUTHORITY_EXIT_HMAC_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)))
	t.Setenv("ROBIN_QUOTE_AUTHORITY_ED25519_PRIVATE_KEY", base64.StdEncoding.EncodeToString(privateKey))
	t.Setenv("ROBIN_COORDINATOR_URL", "http://127.0.0.1:8080")
	t.Setenv("ROBIN_COORDINATOR_MARKET_CALLER", "quote-publisher")
	t.Setenv("ROBIN_COORDINATOR_MARKET_HMAC_KEY", hex.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	t.Setenv("ROBIN_COORDINATOR_EPISODE_CALLER", "episode-resolver")
	t.Setenv("ROBIN_COORDINATOR_EPISODE_HMAC_KEY", hex.EncodeToString(bytes.Repeat([]byte{4}, 32)))
	t.Setenv("ROBIN_LIGHTER_AAPL_MARKET_INDEX", "101")
	t.Setenv("LIGHTER_AAPL_BASE_DECIMALS", "4")
	t.Setenv("LIGHTER_AAPL_PRICE_DECIMALS", "3")
	t.Setenv("ROBINHOOD_RPC_URL", "https://rpc-one.invalid")
	t.Setenv("ROBINHOOD_RECONCILIATION_RPC_URL", "https://rpc-two.invalid")
	t.Setenv("LIGHTER_API_URL", "https://mainnet.zklighter.elliot.ai")
	t.Setenv("AAPL_REFERENCE_FEED", "0x1111111111111111111111111111111111111111")
	t.Setenv("AAPL_REFERENCE_FEED_CODE_HASH", "0x"+hex.EncodeToString(bytes.Repeat([]byte{5}, 32)))
	t.Setenv("AAPL_REFERENCE_FEED_DECIMALS", "8")
	t.Setenv("AAPL_REFERENCE_FEED_HEARTBEAT_SECONDS", "90000")
}
