package scheduler

import (
	"strings"
	"testing"
)

func TestConfigIsDisabledByDefault(t *testing.T) {
	t.Setenv("ROBIN_LIVE_SCHEDULER_ENABLED", "")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled {
		t.Fatal("scheduler enabled without an explicit flag")
	}
}

func TestEnabledConfigRequiresReviewedLighterMarket(t *testing.T) {
	t.Setenv("ROBIN_LIVE_SCHEDULER_ENABLED", "true")
	t.Setenv("ROBIN_LIVE_SCHEDULER_LIGHTER_AAPL_MARKET_INDEX", "")
	_, err := LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "LIGHTER_AAPL_MARKET_INDEX") {
		t.Fatalf("expected pinned market error, got %v", err)
	}
}
