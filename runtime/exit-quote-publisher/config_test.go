package exitquote

import "testing"

func TestDisabledConfigurationHasNoSecrets(t *testing.T) {
	t.Setenv("ROBIN_EXIT_QUOTE_PUBLISHER_ENABLED", "false")
	config, err := LoadConfig()
	if err != nil || config.Enabled || len(config.QuoteKey) != 0 {
		t.Fatalf("disabled configuration is not inert: %+v %v", config, err)
	}
}

func TestPublicPlaintextAuthorityIsRejected(t *testing.T) {
	if err := validateServiceURL("http://quotes.example/v1"); err == nil {
		t.Fatal("public plaintext quote authority was accepted")
	}
	if err := validateServiceURL("http://robin-quote-authority:8080"); err != nil {
		t.Fatalf("private Render host rejected: %v", err)
	}
}
