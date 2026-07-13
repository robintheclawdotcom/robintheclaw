package quoteauthority

import "testing"

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
