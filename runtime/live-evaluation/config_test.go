package evaluation

import "testing"

func setMarketBootstrapEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ROBIN_LIVE_EVALUATION_LIGHTER_AAPL_BASE_DECIMALS", "4")
	t.Setenv("ROBIN_LIVE_EVALUATION_LIGHTER_AAPL_PRICE_DECIMALS", "3")
	t.Setenv("ROBIN_LIVE_EVALUATION_SPOT_CONFIG_VERSION", "1")
	t.Setenv("ROBIN_LIVE_EVALUATION_UI_MULTIPLIER_E18", "1000000000000000000")
	t.Setenv("ROBIN_LIVE_EVALUATION_MAX_PRICE_DEVIATION_BPS", "100")
	t.Setenv("ROBIN_LIVE_EVALUATION_MAX_UNWIND_PRICE_DEVIATION_BPS", "2500")
	t.Setenv("ROBIN_LIVE_EVALUATION_MARKET_VALID_FROM", "2026-01-01T00:00:00Z")
	t.Setenv("ROBIN_LIVE_EVALUATION_MARKET_VALID_UNTIL", "2027-01-01T00:00:00Z")
}

func TestLoadConfigDisabledHasNoSecretDependencies(t *testing.T) {
	t.Setenv("ROBIN_LIVE_EVALUATION_ENABLED", "false")
	t.Setenv("ROBIN_LIVE_EVALUATION_RESEARCH_DATABASE_URL", "invalid")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled {
		t.Fatal("disabled configuration was enabled")
	}
}

func TestLoadConfigRequiresDistinctDatabasesAndPinnedPolicy(t *testing.T) {
	t.Setenv("ROBIN_LIVE_EVALUATION_ENABLED", "true")
	t.Setenv("ROBIN_LIVE_EVALUATION_RESEARCH_DATABASE_URL", "postgres://localhost/research")
	t.Setenv("ROBIN_LIVE_EVALUATION_PRODUCT_DATABASE_URL", "postgres://localhost/product")
	t.Setenv("ROBIN_LIVE_EVALUATION_EXECUTION_DATABASE_URL", "postgres://localhost/execution")
	t.Setenv("ROBIN_LIVE_EVALUATION_WORKER_ID", "live-evaluation-1")
	t.Setenv("AAPL_MINIMUM_NET_EDGE_PPM", "2000")
	t.Setenv("ROBIN_LIVE_EVALUATION_LIGHTER_AAPL_MARKET_INDEX", "101")
	setMarketBootstrapEnv(t)
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled || config.MinimumNetEdgePPM != 2_000 || config.LighterMarket != 101 ||
		config.MarketBootstrap.ExpectedBaseDecimals != 4 || config.MarketBootstrap.MaxPriceDeviationBPS != 100 {
		t.Fatal("enabled configuration was not pinned")
	}
	t.Setenv("ROBIN_LIVE_EVALUATION_PRODUCT_DATABASE_URL", "postgres://localhost/research")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("shared source databases were accepted")
	}
}
