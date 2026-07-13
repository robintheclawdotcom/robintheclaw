package strategyrunner

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestConfigDefaultsToDisabled(t *testing.T) {
	t.Setenv("ROBIN_STRATEGY_RUNNER_ENABLED", "")
	t.Setenv("ROBIN_COORDINATOR_URL", "")
	t.Setenv("ROBIN_COORDINATOR_INTENT_CALLER", "")
	t.Setenv("ROBIN_COORDINATOR_INTENT_HMAC_KEY", "")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled {
		t.Fatal("strategy runner enabled by default")
	}
}

func TestEnabledConfigRequiresDistinctCoordinatorAuth(t *testing.T) {
	setEnabledConfig(t)
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.CoordinatorURL != "http://127.0.0.1:8080" || config.CoordinatorCaller != "strategy-runner" || len(config.CoordinatorKey) != 32 {
		t.Fatal("coordinator persistence config was not loaded")
	}

	t.Setenv("ROBIN_COORDINATOR_INTENT_HMAC_KEY", hex.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	if _, err := LoadConfig(); err == nil {
		t.Fatal("shared inbound and coordinator HMAC key accepted")
	}
}

func TestPartialCoordinatorConfigIsRejectedWhileDisabled(t *testing.T) {
	t.Setenv("ROBIN_STRATEGY_RUNNER_ENABLED", "false")
	t.Setenv("ROBIN_COORDINATOR_URL", "https://coordinator.example")
	t.Setenv("ROBIN_COORDINATOR_INTENT_CALLER", "")
	t.Setenv("ROBIN_COORDINATOR_INTENT_HMAC_KEY", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("partial coordinator config accepted")
	}
}

func setEnabledConfig(t *testing.T) {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
	t.Setenv("ROBIN_STRATEGY_RUNNER_ENABLED", "true")
	t.Setenv("ROBIN_STRATEGY_RUNNER_CALLER", "evaluation-service")
	t.Setenv("ROBIN_STRATEGY_RUNNER_HMAC_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	t.Setenv("ROBIN_QUOTE_AUTHORITY_ED25519_PUBLIC_KEY", base64.StdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)))
	t.Setenv("ROBIN_COORDINATOR_URL", "http://127.0.0.1:8080")
	t.Setenv("ROBIN_COORDINATOR_INTENT_CALLER", "strategy-runner")
	t.Setenv("ROBIN_COORDINATOR_INTENT_HMAC_KEY", hex.EncodeToString(bytes.Repeat([]byte{2}, 32)))
}
