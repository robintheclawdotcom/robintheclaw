package publisher

import (
	"strings"
	"testing"
)

func TestEnabledConfigLoadsDirectlyFromEnvironment(t *testing.T) {
	values := map[string]string{
		"ACCOUNT_PUBLISHER_ENABLED":                        "true",
		"ACCOUNT_PUBLISHER_COORDINATOR_DATABASE_URL":       "postgres://coordinator.invalid/read-only",
		"ACCOUNT_PUBLISHER_ROBINHOOD_DATABASE_URL":         "postgres://custody.invalid/read-only",
		"ACCOUNT_PUBLISHER_ROBINHOOD_JOURNAL_DATABASE_URL": "postgres://journal.invalid/read-only",
		"ACCOUNT_PUBLISHER_PRIMARY_RPC_URL":                "https://rpc-one.invalid",
		"ACCOUNT_PUBLISHER_SECONDARY_RPC_URL":              "https://rpc-two.invalid",
		"ACCOUNT_PUBLISHER_LIGHTER_BRIDGE_URL":             "http://lighter.internal",
		"LIGHTER_PUBLISHER_BRIDGE_CALLER_ID":               "account-publisher",
		"LIGHTER_PUBLISHER_BRIDGE_HMAC_KEY":                strings.Repeat("aa", 32),
		"ACCOUNT_PUBLISHER_COORDINATOR_URL":                "http://coordinator.internal",
		"ACCOUNT_PUBLISHER_COORDINATOR_CALLER_ID":          "account-publisher",
		"ACCOUNT_PUBLISHER_COORDINATOR_HMAC_KEY":           strings.Repeat("bb", 32),
		"ACCOUNT_PUBLISHER_APPLICATION_URL":                "http://application.internal",
		"ACCOUNT_PUBLISHER_APPLICATION_CALLER_ID":          "account-publisher",
		"ACCOUNT_PUBLISHER_APPLICATION_HMAC_KEY":           strings.Repeat("cc", 32),
		"ACCOUNT_PUBLISHER_LIGHTER_MARKET_ID":              "101",
		"ACCOUNT_PUBLISHER_MINIMUM_COLLATERAL_RAW":         "50",
		"ACCOUNT_PUBLISHER_MINIMUM_SETTLEMENT_RAW":         "25000000",
		"ACCOUNT_PUBLISHER_MINIMUM_OWNER_GAS_RAW":          "1",
		"ACCOUNT_PUBLISHER_MINIMUM_SIGNER_GAS_RAW":         "1",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	t.Setenv("ACCOUNT_PUBLISHER_CONFIG_FILE", "/does/not/exist")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.LighterMarketID != 101 || config.LighterBridge.HMACKey != strings.Repeat("aa", 32) {
		t.Fatalf("unexpected config: %+v", config)
	}
}

func TestConfigRequiresDynamicAuthoritativeSources(t *testing.T) {
	config := validTestConfig()
	config.CoordinatorDatabaseURL = ""
	if err := validateConfig(config); err == nil {
		t.Fatal("missing coordinator account source was accepted")
	}
	config = validTestConfig()
	config.RobinhoodDatabaseURL = ""
	if err := validateConfig(config); err == nil {
		t.Fatal("missing custody account source was accepted")
	}
	config = validTestConfig()
	config.RobinhoodJournalDatabaseURL = ""
	if err := validateConfig(config); err == nil {
		t.Fatal("missing signer journal source was accepted")
	}
}

func TestConfigRejectsUnsafeGlobalMinimums(t *testing.T) {
	config := validTestConfig()
	config.MinimumCollateralRaw = "49.99"
	if err := validateConfig(config); err == nil {
		t.Fatal("unsafe collateral minimum was accepted")
	}
}

func validTestConfig() Config {
	return Config{
		CoordinatorDatabaseURL:      "postgres://coordinator.invalid/read-only",
		RobinhoodDatabaseURL:        "postgres://custody.invalid/read-only",
		RobinhoodJournalDatabaseURL: "postgres://journal.invalid/read-only",
		LighterMarketID:             5,
		MinimumCollateralRaw:        "50",
		MinimumSettlementRaw:        "25000000",
		MinimumOwnerGasRaw:          "1",
		MinimumSignerGasRaw:         "1",
		LighterBridge:               EndpointConfig{URL: "https://lighter.internal", Caller: "account-publisher", HMACKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Coordinator:                 EndpointConfig{URL: "https://coordinator.internal", Caller: "account-publisher", HMACKey: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		Application:                 EndpointConfig{URL: "https://application.internal", Caller: "account-publisher", HMACKey: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
	}
}

func configTestAccount(id string, lighterIndex uint64, vault string) AccountBinding {
	return AccountBinding{
		ExecutionAccountID: id,
		ReadinessAccountID: id,
		Lighter:            LighterBinding{AccountIndex: lighterIndex, APIKeyIndex: 4, MarketID: 5, MinimumCollateralRaw: "50"},
		Robinhood: RobinhoodBinding{
			Registry: "0x3333333333333333333333333333333333333333", Factory: "0x4444444444444444444444444444444444444444",
			Vault: vault, RiskManager: "0x5555555555555555555555555555555555555555", SpotAdapter: "0x6666666666666666666666666666666666666666",
			Owner: "0x7777777777777777777777777777777777777777", Signer: "0x8888888888888888888888888888888888888888",
			VaultCodeHash:        "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			MinimumSettlementRaw: "25000000", MinimumOwnerGasRaw: "1", MinimumSignerGasRaw: "1",
			ReceiptHashes: []string{"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		},
	}
}
