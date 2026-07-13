package strategyrunner

import "testing"

func TestConfigDefaultsToDisabled(t *testing.T) {
	t.Setenv("ROBIN_STRATEGY_RUNNER_ENABLED", "")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled {
		t.Fatal("strategy runner enabled by default")
	}
}
