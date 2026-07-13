package main

import "testing"

func TestDisabledConfigurationHasNoSecretDependencies(t *testing.T) {
	t.Setenv("ROBINHOOD_PROVISIONER_ENABLED", "false")
	configuration, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Enabled {
		t.Fatal("disabled provisioner was enabled")
	}
}

func TestProvisionerRejectsSharedBridgeKey(t *testing.T) {
	t.Setenv("ROBINHOOD_PROVISIONER_ENABLED", "true")
	t.Setenv("ROBINHOOD_PROVISIONER_HMAC_KEY", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	t.Setenv("ROBINHOOD_SIGNER_BRIDGE_HMAC_KEY", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if _, err := loadConfig(); err == nil {
		t.Fatal("shared product and signer HMAC key was accepted")
	}
}
